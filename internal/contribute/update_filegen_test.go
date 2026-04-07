package contribute

import (
	"encoding/json"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// makeTestTitle builds a minimal TitleInfo for update merge tests.
// Attributes mirror the convention used in testScan():
//
//	2  = Name
//	9  = Duration
//	10 = SizeHuman
//	11 = SizeBytes
//	16 = SourceFile / SegmentMap
func makeTestTitle(index int, sourceFile string) makemkv.TitleInfo {
	return makemkv.TitleInfo{
		Index: index,
		Attributes: map[int]string{
			2:  "Title " + sourceFile,
			9:  "1:00:00",
			10: "10.0 GB",
			11: "10000000000",
			16: sourceFile,
		},
	}
}

// makeTestTitleWithStreams builds a TitleInfo that also carries one audio stream.
func makeTestTitleWithStreams(index int, sourceFile string) makemkv.TitleInfo {
	t := makeTestTitle(index, sourceFile)
	t.Streams = []makemkv.StreamInfo{
		{TitleIndex: index, StreamIndex: 0, Attributes: map[int]string{
			1: "A_AC3", 6: "AC3", 3: "eng", 4: "English",
		}},
	}
	return t
}

// existingDiscJSON returns a minimal disc JSON string for testing.
func existingDiscJSON(titles []DiscTitleJSON) string {
	disc := DiscJSON{
		Index:       1,
		Slug:        "blu-ray",
		Name:        "Blu-ray",
		Format:      "Blu-ray",
		ContentHash: "abc123",
		Titles:      titles,
	}
	data, err := json.Marshal(disc)
	if err != nil {
		panic("existingDiscJSON: " + err.Error())
	}
	return string(data)
}

// TestMergeDiscJSON_updatesExistingTitleItem verifies that when a scan title
// matches an existing title (by SourceFile), the Item.Type and Item.Title are
// overwritten by the user label while Tracks, Chapters, and disc-level fields
// (ContentHash) are preserved.
func TestMergeDiscJSON_updatesExistingTitleItem(t *testing.T) {
	existingTitles := []DiscTitleJSON{
		{
			Index:       0,
			SourceFile:  "00001.mpls",
			SegmentMap:  "00001.mpls",
			Duration:    "2:00:00",
			Size:        20000000000,
			DisplaySize: "20.0 GB",
			Item: &DiscTitleItemJSON{
				Type:     "Extra",
				Title:    "Old Name",
				Chapters: []ChapterJSON{{Number: 1, Title: "Ch1"}},
			},
			Tracks: []TrackJSON{
				{Index: 0, Type: "video", Name: "AVC"},
			},
		},
	}

	scan := &makemkv.DiscScan{
		Titles: []makemkv.TitleInfo{makeTestTitle(0, "00001.mpls")},
	}

	labels := []TitleLabel{
		{TitleIndex: 0, Type: "Trailer", Name: "My Trailer"},
	}

	got, err := MergeDiscJSON(existingDiscJSON(existingTitles), scan, labels)
	if err != nil {
		t.Fatalf("MergeDiscJSON returned error: %v", err)
	}

	// Disc-level fields must be preserved.
	if got.ContentHash != "abc123" {
		t.Errorf("ContentHash: want %q, got %q", "abc123", got.ContentHash)
	}

	if len(got.Titles) != 1 {
		t.Fatalf("expected 1 title, got %d", len(got.Titles))
	}

	title := got.Titles[0]

	// Item.Type and Item.Title must be updated.
	if title.Item == nil {
		t.Fatal("Item is nil after merge")
	}
	if title.Item.Type != "Trailer" {
		t.Errorf("Item.Type: want %q, got %q", "Trailer", title.Item.Type)
	}
	if title.Item.Title != "My Trailer" {
		t.Errorf("Item.Title: want %q, got %q", "My Trailer", title.Item.Title)
	}

	// Tracks must be preserved from existing JSON.
	if len(title.Tracks) != 1 || title.Tracks[0].Name != "AVC" {
		t.Errorf("Tracks not preserved: got %+v", title.Tracks)
	}

	// Chapters must be preserved from existing JSON.
	if title.Item.Chapters == nil || len(title.Item.Chapters) != 1 || title.Item.Chapters[0].Title != "Ch1" {
		t.Errorf("Chapters not preserved: got %+v", title.Item.Chapters)
	}
}

// TestMergeDiscJSON_appendsNewTitle verifies that a title present in the scan
// but absent from the existing JSON is appended when its label has a non-empty Type.
func TestMergeDiscJSON_appendsNewTitle(t *testing.T) {
	existingTitles := []DiscTitleJSON{
		{
			Index:      0,
			SourceFile: "00001.mpls",
			Item:       &DiscTitleItemJSON{Type: "MainMovie", Title: "Feature", Chapters: []ChapterJSON{}},
			Tracks:     []TrackJSON{},
		},
	}

	scan := &makemkv.DiscScan{
		Titles: []makemkv.TitleInfo{
			makeTestTitle(0, "00001.mpls"),
			makeTestTitleWithStreams(1, "00800.mpls"),
		},
	}

	labels := []TitleLabel{
		{TitleIndex: 0, Type: "MainMovie", Name: "Feature"},
		{TitleIndex: 1, Type: "Extra", Name: "Behind the Scenes"},
	}

	got, err := MergeDiscJSON(existingDiscJSON(existingTitles), scan, labels)
	if err != nil {
		t.Fatalf("MergeDiscJSON returned error: %v", err)
	}

	if len(got.Titles) != 2 {
		t.Fatalf("expected 2 titles, got %d", len(got.Titles))
	}

	// Find the new title by SourceFile.
	var newTitle *DiscTitleJSON
	for i := range got.Titles {
		if got.Titles[i].SourceFile == "00800.mpls" {
			newTitle = &got.Titles[i]
			break
		}
	}
	if newTitle == nil {
		t.Fatal("new title with SourceFile 00800.mpls not found")
	}
	if newTitle.Item == nil || newTitle.Item.Type != "Extra" {
		t.Errorf("new title Item.Type: want %q, got %v", "Extra", newTitle.Item)
	}
}

// TestMergeDiscJSON_preservesTitlesNotInScan verifies that titles in the existing
// JSON that have no corresponding scan title are kept verbatim in the output.
func TestMergeDiscJSON_preservesTitlesNotInScan(t *testing.T) {
	existingTitles := []DiscTitleJSON{
		{
			Index:      0,
			SourceFile: "00001.mpls",
			Item:       &DiscTitleItemJSON{Type: "MainMovie", Title: "Feature", Chapters: []ChapterJSON{}},
			Tracks:     []TrackJSON{},
		},
		{
			Index:      1,
			SourceFile: "00800.mpls",
			Item:       &DiscTitleItemJSON{Type: "Extra", Title: "Deleted Scenes", Chapters: []ChapterJSON{}},
			Tracks:     []TrackJSON{},
		},
	}

	// Scan has only the first title; second is absent.
	scan := &makemkv.DiscScan{
		Titles: []makemkv.TitleInfo{makeTestTitle(0, "00001.mpls")},
	}

	labels := []TitleLabel{
		{TitleIndex: 0, Type: "MainMovie", Name: "Feature"},
	}

	got, err := MergeDiscJSON(existingDiscJSON(existingTitles), scan, labels)
	if err != nil {
		t.Fatalf("MergeDiscJSON returned error: %v", err)
	}

	if len(got.Titles) != 2 {
		t.Fatalf("expected 2 titles (both preserved), got %d", len(got.Titles))
	}

	// Verify the preserved title is intact.
	found := false
	for _, title := range got.Titles {
		if title.SourceFile == "00800.mpls" {
			found = true
			if title.Item == nil || title.Item.Title != "Deleted Scenes" {
				t.Errorf("preserved title Item.Title: want %q, got %v", "Deleted Scenes", title.Item)
			}
		}
	}
	if !found {
		t.Error("title with SourceFile 00800.mpls was not preserved")
	}
}

// TestMergeDiscJSON_omittedLabelsAreSkipped verifies that a new title whose
// label has an empty Type is not appended to the output.
func TestMergeDiscJSON_omittedLabelsAreSkipped(t *testing.T) {
	// Existing disc has no titles.
	existingTitles := []DiscTitleJSON{}

	scan := &makemkv.DiscScan{
		Titles: []makemkv.TitleInfo{makeTestTitle(0, "00001.mpls")},
	}

	// Label with empty Type — should be skipped.
	labels := []TitleLabel{
		{TitleIndex: 0, Type: "", Name: ""},
	}

	got, err := MergeDiscJSON(existingDiscJSON(existingTitles), scan, labels)
	if err != nil {
		t.Fatalf("MergeDiscJSON returned error: %v", err)
	}

	if len(got.Titles) != 0 {
		t.Errorf("expected 0 titles (omitted label skipped), got %d", len(got.Titles))
	}
}
