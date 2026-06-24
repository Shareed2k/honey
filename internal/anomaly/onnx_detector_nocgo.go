//go:build !cgo

package anomaly

import "errors"

func newONNXDetector(_, _ string, _ float64, _ int) (Detector, error) {
	return nil, errors.New("ONNX anomaly detection unavailable: binary built without CGO")
}
