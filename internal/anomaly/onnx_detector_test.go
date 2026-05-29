package anomaly

import "testing"

func TestEncodeDistilBERTPadsAndMasks(t *testing.T) {
	vocab := map[string]int64{
		"[PAD]": 0,
		"[UNK]": 1,
		"[CLS]": 2,
		"[SEP]": 3,
		"error": 4,
	}
	ids, mask, typeIDs := encodeDistilBERT(vocab, "error", 8)
	if len(ids) != 8 || len(mask) != 8 || len(typeIDs) != 8 {
		t.Fatalf("unexpected lengths: ids=%d mask=%d type=%d", len(ids), len(mask), len(typeIDs))
	}
	if ids[0] != 2 || ids[1] != 4 || ids[2] != 3 {
		t.Fatalf("unexpected ids prefix: %v", ids[:3])
	}
	if mask[0] != 1 || mask[1] != 1 || mask[2] != 1 || mask[3] != 0 {
		t.Fatalf("unexpected mask: %v", mask)
	}
}

func TestWordPieceTokenize(t *testing.T) {
	vocab := map[string]int64{
		"play":  1,
		"##ing": 2,
		"[UNK]": 3,
		"[CLS]": 4,
		"[SEP]": 5,
		"[PAD]": 6,
	}
	got := wordPieceTokenize("playing", vocab)
	if len(got) != 2 || got[0] != "play" || got[1] != "##ing" {
		t.Fatalf("wordpiece = %v", got)
	}
}
