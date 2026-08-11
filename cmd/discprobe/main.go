// Command discprobe reports what a Blu-ray disc's payload actually looks like,
// so a real disc can settle questions that synthetic test fixtures cannot.
//
// BluForge decides whether to spend ~100GB recovering a disc based on whether
// its MPEG-TS packets are scrambled. The classifier is exercised entirely
// against fixtures generated from the AACS and BDAV specifications — which
// means the tests agree with the implementation's understanding of the format,
// including anywhere that understanding is wrong. Running this against real
// discs is what closes that gap.
//
// The output is structural metadata and packet headers only. No payload bytes
// are read into the report, so it is safe to share.
//
// Build a static Linux binary from a development machine:
//
//	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o discprobe ./cmd/discprobe
//
// Then run it against a mounted disc, or against a `makemkvcon backup` folder:
//
//	discprobe -root /mnt/disc
//	discprobe -root /mnt/disc -trace -out sg1-s4d3.json
//
// -device mounts the disc first, which needs privileges the plain -root form
// does not.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/johnpostlethwait/bluforge/internal/aacs"
	"github.com/johnpostlethwait/bluforge/internal/mpls"
)

func main() {
	root := flag.String("root", "", "path to a mounted disc, or a makemkvcon backup folder")
	device := flag.String("device", "", "device to mount and inspect, e.g. /dev/sr0 (needs mount privileges)")
	outPath := flag.String("out", "", "write the JSON report to this file instead of stdout")
	withTrace := flag.Bool("trace", false, "include per-packet header bytes (8 per packet, no payload)")
	flag.Parse()

	if *root == "" && *device == "" {
		fmt.Fprintln(os.Stderr, "discprobe: give -root <mounted disc path> or -device <e.g. /dev/sr0>")
		flag.Usage()
		os.Exit(2)
	}

	discRoot := *root
	if discRoot == "" {
		mounted, cleanup, err := mpls.OpenDiscRoot(*device)
		if err != nil {
			fmt.Fprintf(os.Stderr, "discprobe: could not open %s: %v\n", *device, err)
			fmt.Fprintln(os.Stderr, "hint: mount the disc yourself and pass -root instead — reading a mounted")
			fmt.Fprintln(os.Stderr, "      tree needs no privileges, whereas mounting does.")
			os.Exit(1)
		}
		defer cleanup()
		discRoot = mounted
	}

	report, err := aacs.Probe(discRoot, *withTrace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "discprobe: %v\n", err)
		os.Exit(1)
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "discprobe: encode report: %v\n", err)
		os.Exit(1)
	}
	data = append(data, '\n')

	if *outPath != "" {
		if err := os.WriteFile(*outPath, data, 0o666); err != nil {
			fmt.Fprintf(os.Stderr, "discprobe: write %s: %v\n", *outPath, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "discprobe: wrote %s\n", *outPath)
	} else {
		os.Stdout.Write(data)
	}

	// A one-line summary on stderr so the result is readable without a JSON
	// viewer, while stdout stays clean for redirection.
	fmt.Fprintf(os.Stderr, "\nverdict: %s\n  %s\n", report.Verdict, report.Reason)
}
