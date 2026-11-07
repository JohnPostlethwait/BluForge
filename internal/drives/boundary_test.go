package drives

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// Every drive hang this project has had came from code reaching into a drive
// outside whatever held it: a mount made beside a scan, blkid reading a disc a
// listing was probing, ddrescue under a poller. Each was fixed where it was
// found and the next one appeared somewhere else. This test is the fence: only
// internal/drives touches a drive, and anything else that tries fails the
// build here rather than wedging a drive in production.
//
// The packages internal/drives is built from are exempt, because they are the
// mechanism: makemkv runs makemkvcon, mpls mounts, ddrescue runs ddrescue.
// Nothing outside internal/drives may call their drive-touching entry points.
func TestOnlyTheDrivesPackageTouchesADrive(t *testing.T) {
	root := moduleRoot(t)

	exempt := map[string]string{
		"internal/drives":   "the owner",
		"internal/makemkv":  "runs makemkvcon for internal/drives",
		"internal/mpls":     "mounts discs for internal/drives",
		"internal/ddrescue": "runs ddrescue for internal/drives",
		"testutil":          "test fixtures, never run by the server",
		"cmd/discprobe":     "a hand-run diagnostic, never run by the server",
	}

	forbiddenCalls := map[string]string{
		"makemkv.NewExecutor":    "build the executor only in internal/drives",
		"mpls.OpenDiscRoot":      "mount a disc through Drive.OpenRoot",
		"mpls.ForceUnmount":      "release a mount through Broker.ReleaseMount",
		"mpls.ReadDiscLanguages": "read a disc's playlists through a held drive",
		"ddrescue.Rescue":        "rescue through Drive.Rescue",
		"unix.Open":              "open a drive only in internal/drives",
		"syscall.Open":           "open a drive only in internal/drives",
	}
	forbiddenPrograms := map[string]bool{
		"blkid": true, "mount": true, "umount": true, "ddrescue": true, "makemkvcon": true,
		"eject": true, "sg_inq": true, "udisksctl": true,
	}

	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && (strings.HasPrefix(d.Name(), ".") || d.Name() == "vendor" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			if _, ok := exempt[rel]; ok {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "_templ.go") {
			return nil
		}

		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Errorf("parse %s: %v", rel, err)
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.BasicLit:
				if n.Kind == token.STRING {
					if v, err := strconv.Unquote(n.Value); err == nil &&
						(strings.Contains(v, "/dev/sr") || strings.Contains(v, "/dev/sg")) {
						t.Errorf("%s: names a drive node %q; only internal/drives may", fset.Position(n.Pos()), v)
					}
				}
			case *ast.SelectorExpr:
				if id, ok := n.X.(*ast.Ident); ok {
					name := id.Name + "." + n.Sel.Name
					if why, bad := forbiddenCalls[name]; bad {
						t.Errorf("%s: %s reaches a drive outside its holder: %s", fset.Position(n.Pos()), name, why)
					}
				}
			case *ast.CallExpr:
				sel, ok := n.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if id, ok := sel.X.(*ast.Ident); !ok || id.Name != "exec" ||
					(sel.Sel.Name != "Command" && sel.Sel.Name != "CommandContext") {
					return true
				}
				for _, arg := range n.Args {
					lit, ok := arg.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					if v, err := strconv.Unquote(lit.Value); err == nil && forbiddenPrograms[filepath.Base(v)] {
						t.Errorf("%s: runs %s; only internal/drives may run a program against a drive",
							fset.Position(n.Pos()), v)
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The fence has to catch what it is for, or it passes by finding nothing.
func TestTheBoundaryCatchesAReachIntoADrive(t *testing.T) {
	src := `package x
import "os/exec"
func f() { exec.Command("blkid", "-p", "/dev/sr1"); mpls.OpenDiscRoot("/dev/sr0") }`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BasicLit:
			if v, _ := strconv.Unquote(n.Value); strings.Contains(v, "/dev/sr") {
				found = append(found, v)
			}
		case *ast.SelectorExpr:
			if id, ok := n.X.(*ast.Ident); ok && id.Name+"."+n.Sel.Name == "mpls.OpenDiscRoot" {
				found = append(found, "mpls.OpenDiscRoot")
			}
		}
		return true
	})
	if len(found) != 3 {
		t.Errorf("found %v, want the two device nodes and the mount", found)
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot find this test's path")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above " + file)
		}
		dir = parent
	}
}
