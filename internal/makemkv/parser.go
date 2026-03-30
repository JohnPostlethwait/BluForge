package makemkv

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
)

// ParseAll reads all lines from r and returns a slice of parsed Events.
// Blank lines are skipped. An error is returned only for I/O problems; parse
// errors for individual lines are silently skipped.
func ParseAll(r io.Reader) ([]Event, error) {
	var events []Event
	var totalLines, skippedLines int
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			continue
		}
		totalLines++
		ev, err := ParseLine(line)
		if err != nil {
			skippedLines++
			continue
		}
		events = append(events, ev)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if skippedLines > 0 {
		slog.Warn("parser: skipped unrecognized lines", "total_lines", totalLines, "parsed", totalLines-skippedLines, "skipped", skippedLines)
	}
	return events, nil
}

// ParseLine parses a single makemkvcon robot-mode output line into an Event.
func ParseLine(line string) (Event, error) {
	idx := strings.IndexByte(line, ':')
	if idx < 0 {
		return Event{}, fmt.Errorf("makemkv: no colon in line: %q", line)
	}
	typ := line[:idx]
	rest := line[idx+1:]

	switch typ {
	case "DRV":
		return parseDRV(rest)
	case "TCOUT", "TCOUNT":
		return parseTCOUT(rest)
	case "CINFO":
		return parseCINFO(rest)
	case "TINFO":
		return parseTINFO(rest)
	case "SINFO":
		return parseSINFO(rest)
	case "MSG":
		return parseMSG(rest)
	case "PRGV":
		return parsePRGV(rest)
	case "PRGT", "PRGC":
		// Progress title/chapter text — informational only, no data to extract.
		return Event{Type: typ}, nil
	default:
		return Event{}, fmt.Errorf("makemkv: unknown line type: %q", typ)
	}
}

// parseCSV splits a comma-separated string, honouring double-quoted fields.
// Within a quoted field, \" is an escaped double-quote and \\ is a literal
// backslash.
func parseCSV(s string) []string {
	var fields []string
	var cur strings.Builder
	inQuote := false

	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case inQuote && ch == '\\' && i+1 < len(s):
			// Backslash escape inside a quoted field.
			i++
			cur.WriteByte(s[i])
		case inQuote && ch == '"':
			inQuote = false
		case !inQuote && ch == '"':
			inQuote = true
		case !inQuote && ch == ',':
			fields = append(fields, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(ch)
		}
	}
	fields = append(fields, cur.String())
	return fields
}

// mustAtoi converts a string to int, returning 0 on error.
func mustAtoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// parseDRV parses: index,visible,enabled,flags,"drivename","discname","device"
func parseDRV(s string) (Event, error) {
	parts := parseCSV(s)
	if len(parts) < 7 {
		return Event{}, fmt.Errorf("makemkv: DRV: expected >=7 fields, got %d", len(parts))
	}
	d := &DriveInfo{
		Index:      mustAtoi(parts[0]),
		Visible:    mustAtoi(parts[1]),
		Enabled:    mustAtoi(parts[2]),
		Flags:      mustAtoi(parts[3]),
		DriveName:  parts[4],
		DiscName:   parts[5],
		DevicePath: parts[6],
	}
	return Event{Type: "DRV", Drive: d}, nil
}

// parseTCOUT parses: count
func parseTCOUT(s string) (Event, error) {
	return Event{Type: "TCOUT", Count: mustAtoi(s)}, nil
}

// parseCINFO parses: attrId,code,"value"
func parseCINFO(s string) (Event, error) {
	parts := parseCSV(s)
	if len(parts) < 3 {
		return Event{}, fmt.Errorf("makemkv: CINFO: expected >=3 fields, got %d", len(parts))
	}
	attrID := mustAtoi(parts[0])
	disc := &DiscInfo{Attributes: map[int]string{attrID: parts[2]}}
	return Event{Type: "CINFO", Disc: disc}, nil
}

// parseTINFO parses: titleIndex,attrId,code,"value"
func parseTINFO(s string) (Event, error) {
	parts := parseCSV(s)
	if len(parts) < 4 {
		return Event{}, fmt.Errorf("makemkv: TINFO: expected >=4 fields, got %d", len(parts))
	}
	titleIdx := mustAtoi(parts[0])
	attrID := mustAtoi(parts[1])
	ti := &TitleInfo{
		Index:      titleIdx,
		Attributes: map[int]string{attrID: parts[3]},
	}
	return Event{Type: "TINFO", Title: ti}, nil
}

// parseSINFO parses: titleIndex,streamIndex,attrId,code,"value"
func parseSINFO(s string) (Event, error) {
	parts := parseCSV(s)
	if len(parts) < 5 {
		return Event{}, fmt.Errorf("makemkv: SINFO: expected >=5 fields, got %d", len(parts))
	}
	si := &StreamInfo{
		TitleIndex:  mustAtoi(parts[0]),
		StreamIndex: mustAtoi(parts[1]),
		Attributes:  map[int]string{mustAtoi(parts[2]): parts[4]},
	}
	return Event{Type: "SINFO", Stream: si}, nil
}

// parseMSG parses: code,flags,count,"text","format","param1",...
func parseMSG(s string) (Event, error) {
	parts := parseCSV(s)
	if len(parts) < 5 {
		return Event{}, fmt.Errorf("makemkv: MSG: expected >=5 fields, got %d", len(parts))
	}
	msg := &Message{
		Code:   mustAtoi(parts[0]),
		Flags:  mustAtoi(parts[1]),
		Count:  mustAtoi(parts[2]),
		Text:   parts[3],
		Format: parts[4],
	}
	if len(parts) > 5 {
		msg.Params = parts[5:]
	}
	return Event{Type: "MSG", Message: msg}, nil
}

// parsePRGV parses: current,total,max
func parsePRGV(s string) (Event, error) {
	parts := parseCSV(s)
	if len(parts) < 3 {
		return Event{}, fmt.Errorf("makemkv: PRGV: expected >=3 fields, got %d", len(parts))
	}
	p := &Progress{
		Current: mustAtoi(parts[0]),
		Total:   mustAtoi(parts[1]),
		Max:     mustAtoi(parts[2]),
	}
	return Event{Type: "PRGV", Progress: p}, nil
}
