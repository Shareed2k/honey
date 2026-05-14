package presign

import "testing"

func TestChoosePartLayout(t *testing.T) {
	const MiB = int64(1) << 20
	const GiB = int64(1) << 30

	tests := []struct {
		name       string
		size       int64
		thresh     int64
		wantSingle bool
		minParts   int
	}{
		{"empty", 0, 64 * MiB, true, 1},
		{"under threshold", 10 * MiB, 64 * MiB, true, 1},
		{"at threshold", 64 * MiB, 64 * MiB, true, 1},
		{"just over", 65 * MiB, 64 * MiB, false, 2},
		{"five gigs", 5 * GiB, 64 * MiB, false, 80},
		{"clamped to 10000 parts", 1 << 40, 64 * MiB, false, 10000}, // 1 TiB
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			layout := choosePartLayout(tc.size, tc.thresh)
			if layout.Single != tc.wantSingle {
				t.Fatalf("single=%v, want %v", layout.Single, tc.wantSingle)
			}
			if tc.wantSingle {
				if layout.PartCount != 1 || layout.PartSize != tc.size {
					t.Fatalf("single layout wrong: count=%d size=%d", layout.PartCount, layout.PartSize)
				}
				return
			}
			if layout.PartCount > 10000 {
				t.Fatalf("PartCount=%d exceeds S3 max (10000)", layout.PartCount)
			}
			if int64(layout.PartCount)*layout.PartSize < tc.size {
				t.Fatalf("PartCount*PartSize=%d < size=%d", int64(layout.PartCount)*layout.PartSize, tc.size)
			}
			if int64(layout.PartCount-1)*layout.PartSize >= tc.size {
				t.Fatalf("layout over-counts parts")
			}
		})
	}
}
