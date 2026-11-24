package backupcopy

import (
	"bufio"
	"context"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const defaultWALSegmentSize = 16 << 20

type barmanMetadata struct {
	walSegmentSize uint64
	tablespaces    []string
}

// Barman serializes tablespaces as a Python list of (name, oid, location)
// tuples. Match the two quoted strings without interpreting their escapes.
var barmanTablespace = regexp.MustCompile(`^\(\s*(?:'[^'\\]*(?:\\.[^'\\]*)*'|"[^"\\]*(?:\\.[^"\\]*)*")\s*,\s*([0-9]+)\s*,\s*(?:'[^'\\]*(?:\\.[^'\\]*)*'|"[^"\\]*(?:\\.[^"\\]*)*")\s*\)`)

func readBarmanMetadata(ctx context.Context, src ObjectStore, key string) (barmanMetadata, error) {
	rc, err := src.Open(ctx, key)
	if err != nil {
		return barmanMetadata{}, fmt.Errorf("opening %q: %w", key, err)
	}
	defer func() { _ = rc.Close() }()
	metadata := barmanMetadata{walSegmentSize: defaultWALSegmentSize}
	scanner := bufio.NewScanner(rc)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	for scanner.Scan() {
		field, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok {
			continue
		}
		switch field {
		case "xlog_segment_size":
			if value == "None" { // Barman's default for older backups.
				continue
			}
			size, err := strconv.ParseUint(value, 10, 64)
			if err != nil || size < 1<<20 || size > 1<<30 || size&(size-1) != 0 {
				return barmanMetadata{}, fmt.Errorf("invalid xlog_segment_size %q in %q", value, key)
			}
			metadata.walSegmentSize = size
		case "tablespaces":
			if value == "None" {
				continue
			}
			if !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") {
				return barmanMetadata{}, fmt.Errorf("invalid tablespaces in %q", key)
			}
			remaining := strings.TrimSpace(value[1 : len(value)-1])
			for remaining != "" {
				match := barmanTablespace.FindStringSubmatch(remaining)
				if match == nil {
					return barmanMetadata{}, fmt.Errorf("invalid tablespace entry in %q", key)
				}
				metadata.tablespaces = append(metadata.tablespaces, match[1])
				remaining = strings.TrimSpace(remaining[len(match[0]):])
				if remaining != "" {
					if remaining[0] != ',' {
						return barmanMetadata{}, fmt.Errorf("invalid tablespace separator in %q", key)
					}
					remaining = strings.TrimSpace(remaining[1:])
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return barmanMetadata{}, fmt.Errorf("reading %q: %w", key, err)
	}
	return metadata, nil
}

func validateBaseArchives(objects []ObjectInfo, prefix string, tablespaces []string) error {
	archives := map[string][]int{"data": nil}
	for _, oid := range tablespaces {
		archives[oid] = nil
	}
	for _, obj := range objects {
		if obj.Size <= 0 || !strings.HasPrefix(obj.Key, prefix) {
			continue
		}
		name := stripCompressionExt(strings.TrimPrefix(obj.Key, prefix))
		stem, ok := strings.CutSuffix(name, ".tar")
		if !ok {
			continue
		}
		archive, suffix, split := strings.Cut(stem, "_")
		if _, ok := archives[archive]; !ok {
			continue
		}
		part := 0
		if split {
			var err error
			part, err = strconv.Atoi(suffix)
			if err != nil || part <= 0 || fmt.Sprintf("%04d", part) != suffix {
				continue
			}
		}
		archives[archive] = append(archives[archive], part)
	}
	for _, archive := range append([]string{"data"}, tablespaces...) {
		parts := archives[archive]
		slices.Sort(parts)
		if len(parts) == 0 || parts[0] != 0 {
			return fmt.Errorf("source base backup %q is missing %s.tar", prefix, archive)
		}
		for index, part := range parts {
			if index != part {
				return fmt.Errorf("source base backup %q has missing or duplicate %s archive parts", prefix, archive)
			}
		}
	}
	return nil
}

type walRange struct {
	timeline, first, last, segmentsPerLog uint64
}

func newWALRange(begin, end string, segmentSize uint64) (walRange, error) {
	beginTimeline, _ := strconv.ParseUint(begin[:8], 16, 32)
	endTimeline, _ := strconv.ParseUint(end[:8], 16, 32)
	if beginTimeline == 0 || beginTimeline != endTimeline {
		// A physical backup on the primary cannot span a promotion. Do not
		// guess a WAL chain across timelines from just the endpoints.
		return walRange{}, fmt.Errorf("backup WAL range must be on one valid timeline: %s..%s", begin, end)
	}
	segmentsPerLog := (uint64(1) << 32) / segmentSize
	first, okFirst := walOrdinal(begin, segmentsPerLog)
	last, okLast := walOrdinal(end, segmentsPerLog)
	if !okFirst || !okLast || first > last {
		return walRange{}, fmt.Errorf("invalid WAL range %s..%s for segment size %d", begin, end, segmentSize)
	}
	return walRange{beginTimeline, first, last, segmentsPerLog}, nil
}

func walOrdinal(name string, segmentsPerLog uint64) (uint64, bool) {
	log, _ := strconv.ParseUint(name[8:16], 16, 32)
	segment, _ := strconv.ParseUint(name[16:], 16, 32)
	return log*segmentsPerLog + segment, segment < segmentsPerLog
}

func (w walRange) selectObjects(prefix string, objects []ObjectInfo) ([]ObjectInfo, error) {
	var selected []ObjectInfo
	found := make(map[uint64]bool)
	timeline := fmt.Sprintf("%08X", w.timeline)
	history := timeline + ".history"
	foundHistory := w.timeline == 1
	for _, obj := range objects {
		key, ok := strings.CutPrefix(obj.Key, prefix)
		if !ok || obj.Size <= 0 {
			continue
		}
		name := stripCompressionExt(path.Base(key))
		if historyTimeline, isHistory := strings.CutSuffix(name, ".history"); isHistory {
			if len(historyTimeline) != 8 || path.Dir(key) != "." {
				continue
			}
			id, err := strconv.ParseUint(historyTimeline, 16, 32)
			if err != nil || id > w.timeline {
				continue
			}
			if name == history {
				foundHistory = true
			}
			selected = append(selected, obj)
			continue
		}
		if !isWALSegmentName(name) || name[:8] != timeline || path.Dir(key) != name[:16] {
			continue
		}
		ordinal, ok := walOrdinal(name, w.segmentsPerLog)
		if ok && ordinal >= w.first && ordinal <= w.last {
			found[ordinal] = true
			selected = append(selected, obj)
		}
	}
	for ordinal := w.first; ordinal <= w.last; ordinal++ {
		if !found[ordinal] {
			kind := "WAL"
			switch ordinal {
			case w.first:
				kind = "beginWal"
			case w.last:
				kind = "endWal"
			}
			return nil, fmt.Errorf("missing required %s segment %q in source WAL archive", kind,
				fmt.Sprintf("%s%08X%08X", timeline, ordinal/w.segmentsPerLog, ordinal%w.segmentsPerLog))
		}
	}
	if !foundHistory {
		return nil, fmt.Errorf("missing required timeline history %q in source WAL archive", history)
	}
	return selected, nil
}
