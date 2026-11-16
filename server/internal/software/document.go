package software

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type directive struct {
	key, value, section string
	start, end          int
}

// Scan Nginx with quote/comment awareness, retaining offsets so unrelated content is untouched.
func nginxDirectives(text string) ([]directive, error) {
	out := []directive{}
	stack := []string{}
	start := -1
	quote := byte(0)
	escape := false
	comment := false
	for i := 0; i < len(text); i++ {
		c := text[i]
		if comment {
			if c == '\n' {
				comment = false
			}
			continue
		}
		if escape {
			escape = false
			continue
		}
		if c == '\\' {
			escape = true
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '#' {
			comment = true
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			if start < 0 {
				start = i
			}
			continue
		}
		if start < 0 && (c == ' ' || c == '\n' || c == '\t' || c == '\r') {
			continue
		}
		switch c {
		case '{':
			if start < 0 {
				return nil, errors.New("无效配置块")
			}
			out = append(out, directive{key: "@block", value: strings.TrimSpace(text[start:i]), section: strings.Join(stack, "/"), start: -1, end: -1})
			stack = append(stack, strings.TrimSpace(text[start:i]))
			start = -1
		case '}':
			if len(stack) == 0 || start >= 0 {
				return nil, errors.New("配置括号不匹配")
			}
			stack = stack[:len(stack)-1]
		case ';':
			if start < 0 {
				return nil, errors.New("空指令")
			}
			s := text[start:i]
			f := strings.Fields(s)
			if len(f) < 2 {
				return nil, errors.New("指令缺少参数")
			}
			out = append(out, directive{f[0], strings.TrimSpace(s[len(f[0]):]), strings.Join(stack, "/"), start, i + 1})
			start = -1
		default:
			if start < 0 {
				start = i
			}
		}
	}
	if quote != 0 || len(stack) > 0 || start >= 0 {
		return nil, errors.New("配置未完整结束")
	}
	return out, nil
}
func lineDirectives(text string) []directive {
	out := []directive{}
	section := ""
	offset := 0
	for _, line := range strings.SplitAfter(text, "\n") {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
			section = strings.Trim(s, "[]")
		} else if s != "" && !strings.HasPrefix(s, "#") && !strings.HasPrefix(s, ";") {
			code := strings.TrimRight(line, "\r\n")
			quote := rune(0)
			cut := len(code)
			for i, c := range code {
				if c == '\'' || c == '"' {
					if quote == 0 {
						quote = c
					} else if quote == c {
						quote = 0
					}
				}
				if quote == 0 && (c == ';' || c == '#') && (i == 0 || code[i-1] == ' ' || code[i-1] == '\t') {
					cut = i
					break
				}
			}
			code = strings.TrimRight(code[:cut], " \t")
			trimmed := strings.TrimLeft(code, " \t")
			indent := len(code) - len(trimmed)
			key, val, ok := strings.Cut(trimmed, "=")
			if !ok {
				parts := strings.SplitN(trimmed, " ", 2)
				key = parts[0]
				if len(parts) == 2 {
					val = parts[1]
				}
			}
			key = strings.TrimSpace(key)
			val = strings.TrimSpace(val)
			if key != "" {
				out = append(out, directive{key, val, section, offset + indent, offset + len(code)})
			}
		}
		offset += len(line)
	}
	return out
}

func directives(sw, text string) ([]directive, error) {
	if sw == "nginx" {
		return nginxDirectives(text)
	}
	return lineDirectives(text), nil
}
func Values(sw, version, file, text string) map[string]string {
	out := map[string]string{}
	ds, err := directives(sw, text)
	if err != nil {
		return out
	}
	for _, f := range Fields(sw, version, file) {
		out[f.Key] = f.Default
		for _, d := range ds {
			if d.key == f.Key && (d.section == f.Section || sw == "redis") {
				out[f.Key] = strings.Trim(d.value, `"`)
			}
		}
	}
	return out
}

// Patch edits known scalar directives only. New Nginx fields are inserted into their existing block.
func Patch(sw, version, file, text string, values map[string]string) (string, error) {
	fields := Fields(sw, version, file)
	if err := ValidateValues(fields, values); err != nil {
		return "", err
	}
	ds, err := directives(sw, text)
	if err != nil {
		return "", err
	}
	type edit struct {
		start, end int
		text       string
	}
	edits := []edit{}
	missing := []Field{}
	for _, f := range fields {
		v, ok := values[f.Key]
		if !ok {
			continue
		}
		found := false
		for _, d := range ds {
			if d.key == f.Key && (d.section == f.Section || sw == "redis") {
				found = true
				replacement := f.Key + " = " + v
				if sw == "nginx" {
					replacement = f.Key + " " + v + ";"
				}
				if sw == "redis" {
					replacement = f.Key + " " + v
				}
				edits = append(edits, edit{d.start, d.end, replacement})
			}
		}
		if !found {
			missing = append(missing, f)
		}
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	for _, e := range edits {
		text = text[:e.start] + e.text + text[e.end:]
	}
	for _, f := range missing {
		v := values[f.Key]
		if sw == "nginx" {
			line := f.Key + " " + v + ";\n"
			if f.Section == "" {
				text = line + text
			} else {
				re := regexp.MustCompile(`(?m)\b` + regexp.QuoteMeta(f.Section) + `\s*\{`)
				loc := re.FindStringIndex(text)
				if loc == nil {
					return "", fmt.Errorf("缺少 %s 配置块", f.Section)
				}
				text = text[:loc[1]] + "\n " + line + text[loc[1]:]
			}
		} else if sw == "redis" {
			text += "\n" + f.Key + " " + v + "\n"
		} else {
			text += "\n[" + f.Section + "]\n" + f.Key + " = " + v + "\n"
		}
	}
	return text, nil
}

// ValidateEdit preserves all non-editable directives including users, paths, modules and includes.
func ValidateEdit(sw, version, file, old, candidate string) error {
	if len(candidate) > 1<<20 || strings.ContainsRune(candidate, 0) {
		return errors.New("配置过大或含有无效字符")
	}
	a, err := directives(sw, old)
	if err != nil {
		return err
	}
	b, err := directives(sw, candidate)
	if err != nil {
		return err
	}
	allowed := map[string]Field{}
	for _, f := range Fields(sw, version, file) {
		allowed[f.Section+"/"+f.Key] = f
	}
	protected := func(ds []directive) ([]string, error) {
		p := []string{}
		seen := map[string]bool{}
		for _, d := range ds {
			k := d.section + "/" + d.key
			f, ok := allowed[k]
			if sw == "redis" {
				f, ok = allowed["/"+d.key]
			}
			if ok {
				if seen[k] {
					return nil, fmt.Errorf("%s 重复定义，请先合并", d.key)
				}
				seen[k] = true
				if err := ValidateValues([]Field{f}, map[string]string{f.Key: strings.Trim(d.value, `"`)}); err != nil {
					return nil, err
				}
			} else {
				p = append(p, k+"="+d.value)
			}
		}
		sort.Strings(p)
		return p, nil
	}
	pa, err := protected(a)
	if err != nil {
		return err
	}
	pb, err := protected(b)
	if err != nil {
		return err
	}
	if strings.Join(pa, "\n") != strings.Join(pb, "\n") {
		return errors.New("不能修改受保护的用户、路径、模块、监听或未知指令；请使用对应管理入口")
	}
	return ValidateValues(Fields(sw, version, file), Values(sw, version, file, candidate))
}
