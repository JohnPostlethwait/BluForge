package drives

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/johnpostlethwait/bluforge/internal/ddrescue"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// Drive is a held drive: the only way to read it. It is valid until the Do
// that handed it out returns.
type Drive struct {
	b      *Broker
	idx    int
	device string

	mu       sync.Mutex
	released bool
	// roots are the mounts opened through OpenRoot, released with the drive.
	roots []openRoot
}

type openRoot struct {
	path    string
	release func()
}

// Index is the drive's index.
func (d *Drive) Index() int { return d.idx }

// Device is the drive's node, for display.
func (d *Drive) Device() string { return d.device }

func (d *Drive) source() makemkv.Source { return makemkv.DeviceSource(d.idx, d.device) }

func (d *Drive) check() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.released {
		return fmt.Errorf("%w: drive %d", ErrReleased, d.idx)
	}
	return nil
}

// release drops every mount opened through the drive and marks it released.
// A mount left live under media that changes is what wedged drives before, so
// none outlives its holder.
func (d *Drive) release() {
	d.mu.Lock()
	roots := d.roots
	d.roots = nil
	d.released = true
	d.mu.Unlock()
	for i := len(roots) - 1; i >= 0; i-- {
		roots[i].release()
	}
	if len(roots) > 0 && d.b.forceUnmount != nil {
		if err := d.b.forceUnmount(d.device); err != nil {
			slog.Error("drives: the disc is still mounted after its holder let go",
				"drive_index", d.idx, "device", d.device, "error", err)
		}
	}
}

// Identify reports the drive and the disc in it, as makemkvcon names them.
func (d *Drive) Identify(ctx context.Context) (makemkv.DriveInfo, error) {
	if err := d.check(); err != nil {
		return makemkv.DriveInfo{}, err
	}
	return d.b.eng.Identify(ctx, d.source())
}

// Scan reads the disc's titles. onEvent may be nil.
func (d *Drive) Scan(ctx context.Context, onEvent func(makemkv.Event)) (*makemkv.DiscScan, error) {
	if err := d.check(); err != nil {
		return nil, err
	}
	return d.b.eng.ScanSourceWithProgress(ctx, d.source(), onEvent)
}

// Rip rips one title.
func (d *Drive) Rip(ctx context.Context, titleID int, expectSource, outputDir string, onEvent func(makemkv.Event), sel *makemkv.SelectionOpts) error {
	if err := d.check(); err != nil {
		return err
	}
	return d.b.eng.StartRip(ctx, d.source(), titleID, expectSource, outputDir, onEvent, sel)
}

// Backup copies the disc to destDir as a raw disc folder.
func (d *Drive) Backup(ctx context.Context, destDir string, onEvent func(makemkv.Event)) error {
	if err := d.check(); err != nil {
		return err
	}
	return d.b.eng.Backup(ctx, d.source(), destDir, onEvent)
}

// OpenRoot mounts the disc and returns its root. The mount is released when
// the holder lets go of the drive, if not before through the returned release.
func (d *Drive) OpenRoot(ctx context.Context) (string, func(), error) {
	if err := d.check(); err != nil {
		return "", func() {}, err
	}
	root, release, err := d.b.openRoot(d.device)
	if err != nil {
		return "", func() {}, err
	}
	var once sync.Once
	rel := func() { once.Do(release) }
	d.mu.Lock()
	d.roots = append(d.roots, openRoot{path: root, release: rel})
	d.mu.Unlock()
	return root, rel, nil
}

// Size returns the size of the disc in bytes, read from the device.
func (d *Drive) Size() (int64, error) {
	if err := d.check(); err != nil {
		return 0, err
	}
	f, err := os.Open(d.device)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return f.Seek(0, io.SeekEnd)
}

// Rescue runs ddrescue from this drive: from the device itself when
// opts.Source is empty, or from a file on a root this drive has open. runner
// may be nil for the real ddrescue.
func (d *Drive) Rescue(ctx context.Context, runner ddrescue.Runner, opts ddrescue.Options, onProgress func(ddrescue.Progress)) error {
	if err := d.check(); err != nil {
		return err
	}
	if opts.Source == "" {
		opts.Source = d.device
	} else if !d.underOpenRoot(opts.Source) {
		return fmt.Errorf("drives: %s is not on a disc root drive %d has open", opts.Source, d.idx)
	}
	if runner == nil {
		runner = d.b.rescuer
	}
	return ddrescue.Rescue(ctx, runner, opts, onProgress)
}

func (d *Drive) underOpenRoot(path string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	clean := filepath.Clean(path)
	for _, r := range d.roots {
		root := filepath.Clean(r.path)
		if clean == root || strings.HasPrefix(clean, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
