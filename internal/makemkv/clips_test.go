package makemkv

import "testing"

func TestTitleClips(t *testing.T) {
	ti := TitleInfo{Attributes: map[int]string{26: "507,519,512"}}
	if got := ti.Clips(); got != "507,519,512" {
		t.Errorf("Clips() = %q, want %q", got, "507,519,512")
	}
}
