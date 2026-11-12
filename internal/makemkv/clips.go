package makemkv

// Clips returns the clips a title plays, in play order (attribute 26), as
// MakeMKV writes them, e.g. "507,519,512,511".
//
// Some discs hide the movie among hundreds of playlists of the same length,
// each playing the same clips in a different order. The clip list is what tells
// them apart, and what forum posts and TheDiscDB identify the real one by. The
// order of the numbers says nothing on its own: the real playlist on such a
// disc is no more likely to be ascending than a decoy.
func (t *TitleInfo) Clips() string {
	return t.Attributes[26]
}
