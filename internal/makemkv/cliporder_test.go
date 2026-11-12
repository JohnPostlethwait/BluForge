package makemkv

import "testing"

func TestClipOrder(t *testing.T) {
	tests := []struct {
		clips string
		want  ClipOrder
	}{
		{"", ClipOrderUnknown},
		{"garbage", ClipOrderUnknown},
		{"5", ClipOrderInOrder},
		{"1,2,3,4", ClipOrderInOrder},
		{"1-5,7,9-12", ClipOrderInOrder},
		{"1, 2, 3", ClipOrderInOrder},
		{"14,3,27,9", ClipOrderShuffled},
		{"1-5,4", ClipOrderShuffled},
		{"1,2,2", ClipOrderShuffled},
		{"5-3", ClipOrderShuffled},
		{"1,x,3", ClipOrderUnknown},
	}
	for _, tt := range tests {
		if got := ClipOrderOf(tt.clips); got != tt.want {
			t.Errorf("ClipOrderOf(%q) = %q, want %q", tt.clips, got, tt.want)
		}
	}
}

func TestTitleClips(t *testing.T) {
	ti := TitleInfo{Attributes: map[int]string{26: "1-3,7"}}
	if got := ti.Clips(); got != "1-3,7" {
		t.Errorf("Clips() = %q, want %q", got, "1-3,7")
	}
}
