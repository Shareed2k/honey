package cuetry

import (
	"fmt"
	"reflect"
	"strings"
	"text/template"

	sprig "github.com/go-task/slim-sprig/v3"
	"github.com/shareed2k/honey/internal/jsonutil"
)

// RenderTemplateOpts configures a template render.
type RenderTemplateOpts struct {
	Template string
	Data     map[string]any
	KV       KVReader
	Funcs    template.FuncMap
}

// OutputTemplateFuncMap returns template helpers for named recipe outputs.
func OutputTemplateFuncMap(capture *RecipeOutputCapture) template.FuncMap {
	return outputTemplateFuncMap(capture)
}

var blockedTemplateFuncs = map[string]struct{}{
	"env": {}, "expandenv": {}, "getHostByName": {},
	"ago": {}, "date": {}, "dateInZone": {}, "dateModify": {}, "date_in_zone": {}, "date_modify": {},
	"duration": {}, "durationRound": {}, "htmlDate": {}, "htmlDateInZone": {},
	"mustDateModify": {}, "mustToDate": {}, "must_date_modify": {}, "now": {}, "toDate": {}, "unixEpoch": {},
	"buildCustomCert": {}, "derivePassword": {}, "genCA": {}, "genPrivateKey": {}, "genSelfSignedCert": {}, "genSignedCert": {},
	"randAlpha": {}, "randAlphaNum": {}, "randAscii": {}, "randBytes": {}, "randInt": {}, "randNumeric": {}, "randString": {}, "uuidv4": {},
}

// RenderTemplate evaluates a Go text/template with slim-sprig.
func RenderTemplate(opts RenderTemplateOpts) (string, error) {
	body := strings.TrimSpace(opts.Template)
	if body == "" {
		return "", fmt.Errorf("cuetry: template body is empty")
	}
	data := opts.Data
	if data == nil {
		data = map[string]any{}
	}
	funcs := templateFuncMap(opts.KV)
	for name, fn := range opts.Funcs {
		funcs[name] = fn
	}
	tmpl, err := template.New("recipe").Option("missingkey=error").Funcs(funcs).Parse(body)
	if err != nil {
		return "", fmt.Errorf("cuetry: template parse: %w", err)
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, data); err != nil {
		return "", fmt.Errorf("cuetry: template execute: %w", err)
	}
	return b.String(), nil
}

func templateFuncMap(kv KVReader) template.FuncMap {
	base := sprig.TxtFuncMap()
	out := make(template.FuncMap, len(base)+16)
	for name, fn := range base {
		if _, blocked := blockedTemplateFuncs[name]; blocked {
			continue
		}
		out[name] = fn
	}
	out["split"] = templateSplit
	out["splitList"] = templateSplit
	out["join"] = templateJoin
	out["count"] = templateCount
	out["add"] = templateAdd
	out["empty"] = templateEmpty
	out["upper"] = strings.ToUpper
	out["lower"] = strings.ToLower
	out["trim"] = strings.TrimSpace
	out["default"] = templateDefault
	out["toJson"] = templateToJSON
	out["kvGet"] = func(key string) string {
		key = strings.TrimSpace(key)
		if key == "" {
			return ""
		}
		if err := stepkvValidateKey(key); err != nil {
			return ""
		}
		if kv == nil {
			return ""
		}
		v, found, err := kv.Get(key)
		if err != nil || !found {
			return ""
		}
		return v
	}
	out["kvHas"] = func(key string) bool {
		key = strings.TrimSpace(key)
		if key == "" {
			return false
		}
		if err := stepkvValidateKey(key); err != nil {
			return false
		}
		if kv == nil {
			return false
		}
		_, found, err := kv.Get(key)
		return err == nil && found
	}
	out["jqGet"] = func(jsonDoc, query string) string {
		val, err := EvalJQ(jsonDoc, query)
		if err != nil {
			return ""
		}
		return val
	}
	return out
}

func templateSplit(sep, s string) []string {
	return strings.Split(s, sep)
}

func templateJoin(sep string, list any) string {
	switch v := list.(type) {
	case []string:
		return strings.Join(v, sep)
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, fmt.Sprint(item))
		}
		return strings.Join(parts, sep)
	case []int:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, fmt.Sprint(item))
		}
		return strings.Join(parts, sep)
	case []int64:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, fmt.Sprint(item))
		}
		return strings.Join(parts, sep)
	case []float64:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, fmt.Sprint(item))
		}
		return strings.Join(parts, sep)
	default:
		rv := reflect.ValueOf(list)
		if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
			return fmt.Sprint(list)
		}
		parts := make([]string, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			parts = append(parts, fmt.Sprint(rv.Index(i).Interface()))
		}
		return strings.Join(parts, sep)
	}
}

func templateCount(v any) int {
	if v == nil {
		return 0
	}
	switch x := v.(type) {
	case string:
		return len(x)
	case []any:
		return len(x)
	case []string:
		return len(x)
	case map[string]any:
		return len(x)
	case map[string]string:
		return len(x)
	case []int:
		return len(x)
	case []int64:
		return len(x)
	case []float64:
		return len(x)
	case []bool:
		return len(x)
	case []map[string]any:
		return len(x)
	default:
		rv := reflect.ValueOf(v)
		switch rv.Kind() {
		case reflect.Slice, reflect.Array, reflect.Map:
			return rv.Len()
		default:
			if s, ok := v.(fmt.Stringer); ok {
				return len(s.String())
			}
			return 1
		}
	}
}

func templateAdd(b, a int) int {
	return a + b
}

func templateEmpty(v any) bool {
	if v == nil {
		return true
	}
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x) == ""
	case []any:
		return len(x) == 0
	case []string:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	case map[string]string:
		return len(x) == 0
	case []int:
		return len(x) == 0
	case []int64:
		return len(x) == 0
	case []float64:
		return len(x) == 0
	case []bool:
		return len(x) == 0
	case []map[string]any:
		return len(x) == 0
	default:
		rv := reflect.ValueOf(v)
		switch rv.Kind() {
		case reflect.Slice, reflect.Array, reflect.Map:
			return rv.Len() == 0
		default:
			return false
		}
	}
}

func templateDefault(def, val any) any {
	if templateEmpty(val) {
		return def
	}
	return val
}

func templateToJSON(v any) string {
	b, err := jsonutil.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
