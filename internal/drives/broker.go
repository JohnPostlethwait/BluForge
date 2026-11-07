// Package drives is the one owner of every optical drive.
//
// Every drive hang in BluForge's history was code reaching into a drive while
// something else was driving it: a mount left live under changed media, two
// makemkvcon processes on one drive, blkid reading a disc a listing was
// probing. Each was fixed where it was found. This package removes the class:
// it is the only code that opens a drive's device nodes or runs a process
// against one, and it lets one holder at a time do so.
//
// Work on a drive runs inside Broker.Do. The *Drive it receives is the ball:
// every read of the drive (makemkvcon, a mount, ddrescue) is a method on it,
// it is valid only until the callback returns, and nothing else touches that
// drive meanwhile. A test in this package fails the build if code elsewhere
// names a device node or runs one of those programs itself.
//
// See docs/superpowers/specs/2026-11-06-drive-ownership-design.md.
package drives

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/ddrescue"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
	"github.com/johnpostlethwait/bluforge/internal/mpls"
)

// ErrOtherDriveHeld reports a holder asking for a second drive. Nothing needs
// two drives at once, and allowing it is how two holders deadlock each other.
var ErrOtherDriveHeld = errors.New("drives: already holding another drive")

// ErrReleased reports a *Drive used after its Do returned.
var ErrReleased = errors.New("drives: the drive was used after it was released")

// ErrUnknownDrive reports a drive index with no device node.
var ErrUnknownDrive = errors.New("drives: no such drive")

// engine is what the broker runs makemkvcon through: *makemkv.Executor, or a
// fake in tests.
type engine interface {
	ScanSourceWithProgress(ctx context.Context, src makemkv.Source, onEvent func(makemkv.Event)) (*makemkv.DiscScan, error)
	StartRip(ctx context.Context, src makemkv.Source, titleID int, expectSource, outputDir string, onEvent func(makemkv.Event), selection *makemkv.SelectionOpts) error
	Backup(ctx context.Context, src makemkv.Source, destDir string, onEvent func(makemkv.Event)) error
	Identify(ctx context.Context, src makemkv.Source) (makemkv.DriveInfo, error)
	Isolated() bool
}

// rootOpener mounts a disc and returns its root and a release.
type rootOpener func(device string) (root string, release func(), err error)

// Broker hands out drives one holder at a time.
type Broker struct {
	eng      engine
	isolated bool

	openRoot rootOpener
	// forceUnmount clears any mount on a device, whoever made it.
	forceUnmount func(device string) error
	// rescuer runs ddrescue.
	rescuer ddrescue.Runner
	// slots lists the drives attached; see Slots.
	slots func() ([]Slot, error)

	mu    sync.Mutex
	lanes map[int]chan struct{}
	// global is held by every holder when commands are not isolated: then each
	// makemkvcon opens every drive, so only one may run at a time.
	global chan struct{}
}

// Option configures a Broker.
type Option func(*Broker)

// WithEngine replaces the makemkvcon executor. For tests.
func WithEngine(e engine) Option { return func(b *Broker) { b.eng = e; b.isolated = e.Isolated() } }

// WithRootOpener replaces the disc mounter. For tests.
func WithRootOpener(fn func(device string) (string, func(), error)) Option {
	return func(b *Broker) { b.openRoot = fn }
}

// WithForceUnmount replaces the mount clearer. For tests.
func WithForceUnmount(fn func(device string) error) Option {
	return func(b *Broker) { b.forceUnmount = fn }
}

// WithRescuer replaces the ddrescue runner. For tests.
func WithRescuer(r ddrescue.Runner) Option { return func(b *Broker) { b.rescuer = r } }

// WithSlots replaces drive enumeration. For tests.
func WithSlots(fn func() ([]Slot, error)) Option { return func(b *Broker) { b.slots = fn } }

// New returns a Broker running makemkvcon through an executor it builds from
// execOpts. isolate is the result of makemkv.ProbeIsolation: whether each
// makemkvcon can be confined to its own drive.
func New(isolate bool, execOpts []makemkv.Option, opts ...Option) *Broker {
	execOpts = append(execOpts, makemkv.WithDriveIsolation(isolate))
	b := &Broker{
		eng:          makemkv.NewExecutor(execOpts...),
		isolated:     isolate,
		openRoot:     mpls.OpenDiscRoot,
		forceUnmount: mpls.ForceUnmount,
		rescuer:      ddrescue.ExecRunner{},
		slots:        sysfsSlots,
		lanes:        make(map[int]chan struct{}),
		global:       make(chan struct{}, 1),
	}
	for _, o := range opts {
		o(b)
	}
	return b
}

// Isolated reports whether work on different drives runs at the same time.
func (b *Broker) Isolated() bool { return b.isolated }

// Slots lists the attached drives, from sysfs. It reads no drive.
func (b *Broker) Slots() ([]Slot, error) { return b.slots() }

// DevicePath returns the device node of drive idx, for display and for keying
// a drive's records. It reads no drive. "" when there is no such drive.
func (b *Broker) DevicePath(idx int) string {
	slots, err := b.slots()
	if err != nil {
		return ""
	}
	for _, s := range slots {
		if s.Index == idx {
			return s.Device
		}
	}
	return ""
}

func (b *Broker) lane(idx int) chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	l, ok := b.lanes[idx]
	if !ok {
		l = make(chan struct{}, 1)
		b.lanes[idx] = l
	}
	return l
}

// holdKey marks a context as running under a hold: the drive held, or
// allDrives for the global token.
type holdKey struct{}

type hold struct {
	drive  *Drive // nil for a detached hold on every drive
	global bool
}

func holdFrom(ctx context.Context) *hold {
	h, _ := ctx.Value(holdKey{}).(*hold)
	return h
}

func acquire(ctx context.Context, ch chan struct{}) error {
	select {
	case ch <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Do runs fn holding drive idx, and nothing else touches that drive until fn
// returns. purpose names the work in the log.
//
// A Do waits for the drive's current holder; ctx cancels the wait. Work on
// other drives runs meanwhile when commands are isolated, and waits when they
// are not, since each makemkvcon would then open every drive.
//
// Called with a ctx already holding idx — a holder calling back into the
// broker for its own drive — fn runs under that same hold: the holder keeps
// the ball. Called holding a different drive, it fails with ErrOtherDriveHeld.
//
// Mounts opened through the *Drive are released when fn returns.
func (b *Broker) Do(ctx context.Context, idx int, purpose string, fn func(ctx context.Context, d *Drive) error) error {
	if h := holdFrom(ctx); h != nil {
		if h.drive != nil && h.drive.idx == idx {
			return fn(ctx, h.drive)
		}
		if h.drive != nil || !b.isolated {
			return fmt.Errorf("%w: drive %d wanted for %q", ErrOtherDriveHeld, idx, purpose)
		}
	}

	device := b.DevicePath(idx)
	if device == "" {
		return fmt.Errorf("%w: %d", ErrUnknownDrive, idx)
	}

	token := b.global
	if b.isolated {
		token = b.lane(idx)
	}
	waitStart := time.Now()
	if err := acquire(ctx, token); err != nil {
		return err
	}
	defer func() { <-token }()

	d := &Drive{b: b, idx: idx, device: device}
	held := time.Now()
	slog.Debug("drives: holding", "drive_index", idx, "device", device, "purpose", purpose,
		"waited", held.Sub(waitStart).String())
	defer func() {
		d.release()
		slog.Debug("drives: released", "drive_index", idx, "purpose", purpose,
			"held", time.Since(held).String())
	}()

	return fn(context.WithValue(ctx, holdKey{}, &hold{drive: d, global: !b.isolated}), d)
}

// detached runs fn for a command that reads a copy on disk rather than a
// drive. Isolated, its makemkvcon sees no drive and needs no hold. Without
// isolation it would open every drive, so it holds every drive.
func (b *Broker) detached(ctx context.Context, fn func(ctx context.Context) error) error {
	if b.isolated {
		return fn(ctx)
	}
	if h := holdFrom(ctx); h != nil {
		if h.global {
			return fn(ctx)
		}
		return fmt.Errorf("%w: every drive wanted for a copy", ErrOtherDriveHeld)
	}
	if err := acquire(ctx, b.global); err != nil {
		return err
	}
	defer func() { <-b.global }()
	return fn(context.WithValue(ctx, holdKey{}, &hold{global: true}))
}

// ReleaseMount takes down any mount on device, whoever holds it. Called when
// the disc leaves the drive: a live filesystem on media that is gone is what
// wedged drives before, and a reader part-way through has already lost it.
func (b *Broker) ReleaseMount(device string) error { return b.forceUnmount(device) }
