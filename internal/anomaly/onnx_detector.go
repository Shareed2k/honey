package anomaly

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"math"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unicode"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/shareed2k/honey/internal/safepath"
)

const maxSeqLen = 128

var (
	ortInitOnce sync.Once
	ortInitErr  error
)

type onnxDetector struct {
	threshold float64
	vocab     map[string]int64
	session   *ort.DynamicAdvancedSession
	mu        sync.Mutex

	inputIDsName    string
	attentionName   string
	tokenTypeName   string
	outputScoreName string
}

func newONNXDetector(modelPath, tokenizerPath string, threshold float64, _ int) (*onnxDetector, error) {
	if strings.TrimSpace(tokenizerPath) == "" {
		tokenizerPath = filepath.Join(filepath.Dir(modelPath), "vocab.txt")
	}
	vocab, err := loadVocab(tokenizerPath)
	if err != nil {
		return nil, fmt.Errorf("load tokenizer vocab: %w", err)
	}

	libDir, err := resolveONNXRuntimeLibraryDir()
	if err != nil {
		return nil, fmt.Errorf("onnx runtime libs: %w", err)
	}
	if err := initONNXEnvironment(sharedLibraryPath(libDir)); err != nil {
		return nil, err
	}

	inputs, outputs, err := ort.GetInputOutputInfo(modelPath)
	if err != nil {
		return nil, fmt.Errorf("inspect model io: %w", err)
	}
	inNames, err := detectDistilBERTInputs(inputs)
	if err != nil {
		return nil, err
	}
	outName, err := detectOutputName(outputs)
	if err != nil {
		return nil, err
	}

	allInputs := []string{inNames.inputIDs, inNames.attentionMask}
	if inNames.tokenType != "" {
		allInputs = append(allInputs, inNames.tokenType)
	}
	sess, err := ort.NewDynamicAdvancedSession(modelPath, allInputs, []string{outName}, nil)
	if err != nil {
		return nil, fmt.Errorf("create onnx session: %w", err)
	}

	return &onnxDetector{
		threshold:       threshold,
		vocab:           vocab,
		session:         sess,
		inputIDsName:    inNames.inputIDs,
		attentionName:   inNames.attentionMask,
		tokenTypeName:   inNames.tokenType,
		outputScoreName: outName,
	}, nil
}

type inputNames struct {
	inputIDs      string
	attentionMask string
	tokenType     string
}

func detectDistilBERTInputs(infos []ort.InputOutputInfo) (inputNames, error) {
	var got inputNames
	for _, in := range infos {
		n := strings.ToLower(strings.TrimSpace(in.Name))
		switch n {
		case "input_ids", "inputids":
			got.inputIDs = in.Name
		case "attention_mask", "attentionmask":
			got.attentionMask = in.Name
		case "token_type_ids", "tokentypeids":
			got.tokenType = in.Name
		}
	}
	if got.inputIDs == "" || got.attentionMask == "" {
		return inputNames{}, fmt.Errorf("model inputs must include input_ids and attention_mask")
	}
	return got, nil
}

func detectOutputName(infos []ort.InputOutputInfo) (string, error) {
	for _, out := range infos {
		n := strings.ToLower(strings.TrimSpace(out.Name))
		if n == "probabilities" || n == "logits" || n == "output_0" || n == "score" {
			return out.Name, nil
		}
	}
	if len(infos) == 0 {
		return "", fmt.Errorf("model exposes no outputs")
	}
	return infos[0].Name, nil
}

func initONNXEnvironment(sharedLib string) error {
	ortInitOnce.Do(func() {
		ort.SetSharedLibraryPath(sharedLib)
		ortInitErr = ort.InitializeEnvironment()
	})
	if ortInitErr != nil {
		return fmt.Errorf("initialize onnx runtime environment: %w", ortInitErr)
	}
	return nil
}

func sharedLibraryPath(libDir string) string {
	name := "libonnxruntime.so"
	switch runtime.GOOS {
	case "windows":
		name = "onnxruntime.dll"
	case "darwin":
		name = "libonnxruntime.dylib"
	}
	return filepath.Join(libDir, name)
}

func (d *onnxDetector) Score(ctx context.Context, line string) (Result, error) {
	n := Normalize(line)
	if n == "" {
		return Result{Score: 0, Anomaly: false, Reason: "empty", Original: line}, nil
	}
	ids, mask, typeIDs := encodeDistilBERT(d.vocab, n, maxSeqLen)

	inputIDs, err := ort.NewTensor(ort.NewShape(1, maxSeqLen), ids)
	if err != nil {
		return Result{}, err
	}
	defer inputIDs.Destroy()
	attentionMask, err := ort.NewTensor(ort.NewShape(1, maxSeqLen), mask)
	if err != nil {
		return Result{}, err
	}
	defer attentionMask.Destroy()

	inputs := []ort.Value{inputIDs, attentionMask}
	if d.tokenTypeName != "" {
		tokTensor, err := ort.NewTensor(ort.NewShape(1, maxSeqLen), typeIDs)
		if err != nil {
			return Result{}, err
		}
		defer tokTensor.Destroy()
		inputs = append(inputs, tokTensor)
	}

	outTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 2))
	if err != nil {
		return Result{}, err
	}
	defer outTensor.Destroy()

	d.mu.Lock()
	err = d.session.Run(inputs, []ort.Value{outTensor})
	d.mu.Unlock()
	if err != nil {
		return Result{}, fmt.Errorf("onnx run: %w", err)
	}
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}
	score := scoreFromModelOutput(outTensor.GetData())
	return Result{Score: score, Anomaly: score >= d.threshold, Reason: "onnx-distilbert", Original: line}, nil
}

func scoreFromModelOutput(v []float32) float64 {
	if len(v) == 0 {
		return 0
	}
	if len(v) == 1 {
		s := float64(v[0])
		if s < 0 || s > 1 {
			s = sigmoid(s)
		}
		return clamp01(s)
	}
	// Assume binary logits [normal, anomaly]
	a := float64(v[len(v)-1])
	b := float64(v[0])
	return clamp01(sigmoid(a - b))
}

func sigmoid(x float64) float64 {
	if x >= 0 {
		z := math.Exp(-x)
		return 1 / (1 + z)
	}
	z := math.Exp(x)
	return z / (1 + z)
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func loadVocab(path string) (map[string]int64, error) {
	data, err := safepath.ReadFile(path)
	if err != nil {
		return nil, err
	}
	vocab := make(map[string]int64)
	s := bufio.NewScanner(bytes.NewReader(data))
	idx := int64(0)
	for s.Scan() {
		tok := strings.TrimSpace(s.Text())
		if tok == "" {
			continue
		}
		vocab[tok] = idx
		idx++
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if _, ok := vocab["[CLS]"]; !ok {
		return nil, fmt.Errorf("vocab missing [CLS]")
	}
	if _, ok := vocab["[SEP]"]; !ok {
		return nil, fmt.Errorf("vocab missing [SEP]")
	}
	if _, ok := vocab["[UNK]"]; !ok {
		return nil, fmt.Errorf("vocab missing [UNK]")
	}
	if _, ok := vocab["[PAD]"]; !ok {
		return nil, fmt.Errorf("vocab missing [PAD]")
	}
	return vocab, nil
}

func encodeDistilBERT(vocab map[string]int64, text string, maxLen int) ([]int64, []int64, []int64) {
	cls := vocab["[CLS]"]
	sep := vocab["[SEP]"]
	pad := vocab["[PAD]"]
	unk := vocab["[UNK]"]

	ids := make([]int64, 0, maxLen)
	ids = append(ids, cls)
	for _, t := range basicTokens(text) {
		for _, wp := range wordPieceTokenize(t, vocab) {
			id, ok := vocab[wp]
			if !ok {
				id = unk
			}
			ids = append(ids, id)
			if len(ids) >= maxLen-1 {
				break
			}
		}
		if len(ids) >= maxLen-1 {
			break
		}
	}
	ids = append(ids, sep)
	if len(ids) > maxLen {
		ids = ids[:maxLen]
		ids[maxLen-1] = sep
	}

	mask := make([]int64, maxLen)
	typeIDs := make([]int64, maxLen)
	for i := 0; i < len(ids) && i < maxLen; i++ {
		mask[i] = 1
	}
	for len(ids) < maxLen {
		ids = append(ids, pad)
	}
	return ids, mask, typeIDs
}

func basicTokens(text string) []string {
	text = strings.ToLower(text)
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		out = append(out, b.String())
		b.Reset()
	}
	for _, r := range text {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case unicode.IsSpace(r):
			flush()
		default:
			flush()
			out = append(out, string(r))
		}
	}
	flush()
	return out
}

func wordPieceTokenize(token string, vocab map[string]int64) []string {
	if _, ok := vocab[token]; ok {
		return []string{token}
	}
	runes := []rune(token)
	if len(runes) == 0 {
		return nil
	}
	var pieces []string
	start := 0
	for start < len(runes) {
		end := len(runes)
		matched := ""
		for end > start {
			sub := string(runes[start:end])
			if start > 0 {
				sub = "##" + sub
			}
			if _, ok := vocab[sub]; ok {
				matched = sub
				break
			}
			end--
		}
		if matched == "" {
			return []string{"[UNK]"}
		}
		pieces = append(pieces, matched)
		start = end
	}
	return pieces
}
