package drivemanager

import (
	"fmt"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

// opticalAccess is what the process can observe about the optical device nodes.
type opticalAccess struct {
	nodesFound []string
	// sgNodes are the generic SCSI nodes makemkvcon actually enumerates through.
	// The diagnosis keys on these: a host with only /dev/sr* has nothing to say
	// about sg access, and must not be reported as a group problem.
	sgNodes       []string
	readable      int
	running       string   // "uid:gid" the process runs as
	owningGroups  []string // groups owning the nodes, e.g. "disk(6)"
	processGroups []string // groups the process belongs to
}

// CheckOpticalAccess logs a diagnosis when optical device nodes exist but none
// of them can be opened.
//
// makemkvcon enumerates drives through /dev/sg*, which are mode 0660 and owned
// by root:disk. A container running as a non-root user outside that group sees
// no drives at all and fails with MSG:5042 plus, on a backup, a bare "Backup
// failed" — none of which mentions groups. Saying it plainly at startup turns
// an afternoon of investigation into a one-line compose change.
func CheckOpticalAccess() {
	if diagnosis := describeOpticalAccess(inspectOpticalNodes()); diagnosis != "" {
		slog.Error("optical drive access problem detected", "diagnosis", diagnosis)
	}
}

// describeOpticalAccess renders a diagnosis, or "" when there is nothing wrong.
//
// Silence matters as much as the message: warning on every start when nothing is
// broken teaches users to ignore the one time it counts.
func describeOpticalAccess(a opticalAccess) string {
	if len(a.sgNodes) == 0 {
		// Nothing to diagnose: either no devices were passed into the container,
		// or this host exposes no generic SCSI nodes. Neither is a group problem.
		return ""
	}
	if a.readable > 0 {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, "found %d generic SCSI node(s) (%s) but none could be opened.",
		len(a.sgNodes), strings.Join(a.sgNodes, ", "))
	if a.running != "" {
		fmt.Fprintf(&b, " This process runs as %s", a.running)
		if len(a.processGroups) > 0 {
			fmt.Fprintf(&b, " with groups [%s]", strings.Join(a.processGroups, ", "))
		}
		b.WriteString(".")
	}
	if len(a.owningGroups) > 0 {
		fmt.Fprintf(&b, " The nodes are owned by group(s) [%s].", strings.Join(a.owningGroups, ", "))
	}
	b.WriteString(" makemkvcon enumerates drives through these nodes, so it will report" +
		" \"no usable optical drives\" (MSG:5042) until the process joins the owning group" +
		" — add `group_add: [6]` (or the GID listed above) to the container.")

	return b.String()
}

// inspectOpticalNodes gathers the facts describeOpticalAccess reports on.
func inspectOpticalNodes() opticalAccess {
	var a opticalAccess

	for _, pattern := range []string{"/dev/sg*", "/dev/sr*"} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		a.nodesFound = append(a.nodesFound, matches...)
	}
	if len(a.nodesFound) == 0 {
		return a
	}

	for _, node := range a.nodesFound {
		// Ownership comes from a stat, which never blocks.
		if gid, ok := fileGroup(node); ok {
			a.owningGroups = appendUnique(a.owningGroups, formatGroup(gid))
		}

		// Only the generic SCSI nodes are opened. Opening /dev/sr* makes the
		// kernel probe the drive for media, which blocks for tens of seconds
		// while a loaded disc spins up — and it tells us nothing extra, since
		// makemkvcon enumerates drives through /dev/sg* and that is the access
		// this check exists to verify.
		if !strings.HasPrefix(filepath.Base(node), "sg") {
			continue
		}
		a.sgNodes = append(a.sgNodes, node)
		f, err := os.Open(node)
		if err == nil {
			a.readable++
			f.Close()
		}
	}

	a.running = fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
	if groups, err := os.Getgroups(); err == nil {
		for _, g := range groups {
			a.processGroups = appendUnique(a.processGroups, fmt.Sprintf("%d", g))
		}
	}

	return a
}

// formatGroup renders a GID with its name when one resolves, e.g. "disk(6)".
func formatGroup(gid uint32) string {
	id := fmt.Sprintf("%d", gid)
	if g, err := user.LookupGroupId(id); err == nil && g.Name != "" {
		return fmt.Sprintf("%s(%s)", g.Name, id)
	}
	return id
}

func appendUnique(list []string, v string) []string {
	for _, existing := range list {
		if existing == v {
			return list
		}
	}
	return append(list, v)
}
