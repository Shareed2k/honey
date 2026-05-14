package inventory

import (
	"strings"

	"github.com/shareed2k/honey/internal/hosts"
)

func processRecordTags(r hosts.Record, key, p, backendName string, stripPrefix bool, blacklist []string, byTag map[string][]string) {
	tagsStr, ok := r.Meta["tags"]
	if !ok || tagsStr == "" {
		return
	}

	for _, tag := range strings.Split(tagsStr, ",") {
		t := strings.TrimSpace(tag)
		if t == "" || isBlacklisted(blacklist, t) {
			continue
		}

		tSanitized := sanitizeLabel(t)

		// 1. Raw tag group
		g := groupPrefix(groupTagPrefix, tSanitized, stripPrefix)
		byTag[g] = append(byTag[g], key)

		// 2. <backend>_<tag> group
		if backendName != "" {
			bg := backendName + "_" + tSanitized
			if !stripPrefix {
				bg = groupTagPrefix + "_" + bg
			}
			byTag[bg] = append(byTag[bg], key)
		}

		// 3. <provider>_<tag> and <provider>_<backend>_<tag> groups
		if p != "x" {
			pg := p + "_" + tSanitized
			if !stripPrefix {
				pg = groupTagPrefix + "_" + pg
			}
			byTag[pg] = append(byTag[pg], key)

			if backendName != "" {
				pbg := p + "_" + backendName + "_" + tSanitized
				if !stripPrefix {
					pbg = groupTagPrefix + "_" + pbg
				}
				byTag[pbg] = append(byTag[pbg], key)
			}
		}
	}
}

func processRecordLabels(r hosts.Record, key, p, backendName string, stripPrefix bool, blacklist []string, byLabel map[string][]string) {
	for mk, mv := range r.Meta {
		if !strings.HasPrefix(mk, "label_") {
			continue
		}
		labelKey := strings.TrimPrefix(mk, "label_")
		labelVal := strings.TrimSpace(mv)
		if labelKey == "" || labelVal == "" || isBlacklisted(blacklist, mk) {
			continue
		}

		vSanitized := sanitizeLabel(labelVal)
		kSanitized := sanitizeLabel(labelKey)

		// 1. Raw label group
		var g string
		if stripPrefix {
			g = vSanitized
		} else {
			g = groupLabelPrefix + "_" + kSanitized + "_" + vSanitized
		}
		byLabel[g] = append(byLabel[g], key)

		// 2. <backend>_<label_value> group
		if backendName != "" {
			var bg string
			if stripPrefix {
				bg = backendName + "_" + vSanitized
			} else {
				bg = groupLabelPrefix + "_" + backendName + "_" + kSanitized + "_" + vSanitized
			}
			byLabel[bg] = append(byLabel[bg], key)
		}

		// 3. <provider>_<label_value> and <provider>_<backend>_<label_value> groups
		if p != "x" {
			var pg string
			if stripPrefix {
				pg = p + "_" + vSanitized
			} else {
				pg = groupLabelPrefix + "_" + p + "_" + kSanitized + "_" + vSanitized
			}
			byLabel[pg] = append(byLabel[pg], key)

			if backendName != "" {
				var pbg string
				if stripPrefix {
					pbg = p + "_" + backendName + "_" + vSanitized
				} else {
					pbg = groupLabelPrefix + "_" + p + "_" + backendName + "_" + kSanitized + "_" + vSanitized
				}
				byLabel[pbg] = append(byLabel[pbg], key)
			}
		}
	}
}
