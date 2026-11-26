package page

import (
	"html/template"
	"strings"
)

// SaidBlock is a block of the text a bundle holds of what a version changes (`ritsu ringi diff`, in
// Markdown): a heading, a paragraph, a list, a table, or lines kept as they are. The page shows each as
// HTML of its own, so that nothing of the text is read as HTML.
type SaidBlock struct {
	Heading int      // 1 for "#", 2 for "##" and below; 0 when it is not a heading
	Text    string   // of a heading or a paragraph, with its `code`
	Items   []string // of a list
	Head    []string // the head of a table
	Rows    [][]string
	Pre     string // lines of a block of code, kept as they are
}

// SaidBlocks reads the text the bundle holds as blocks. Comments (<!-- … -->) are left out; what is
// not a heading, a list, a table or a block of code is a paragraph.
func (k AdminBundleKind) SaidBlocks() []SaidBlock {
	return saidBlocks(k.Said)
}

func saidBlocks(text string) (out []SaidBlock) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var para []string
	flush := func() {
		if len(para) > 0 {
			out = append(out, SaidBlock{Text: strings.Join(para, " ")})
			para = nil
		}
	}
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \t")
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			flush()
		case strings.HasPrefix(trimmed, "<!--"):
			flush()
			for !strings.Contains(lines[i], "-->") && i+1 < len(lines) {
				i++
			}
		case strings.HasPrefix(trimmed, "```"):
			flush()
			var pre []string
			for i+1 < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i+1]), "```") {
				i++
				pre = append(pre, lines[i])
			}
			i++ // the closing fence
			out = append(out, SaidBlock{Pre: strings.Join(pre, "\n")})
		case strings.HasPrefix(trimmed, "#"):
			flush()
			level := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
			if rest := trimmed[level:]; strings.HasPrefix(rest, " ") {
				out = append(out, SaidBlock{Heading: min(level, 2), Text: strings.TrimSpace(rest)})
				continue
			}
			para = append(para, trimmed)
		case strings.HasPrefix(trimmed, "|"):
			flush()
			var rows [][]string
			for ; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|"); i++ {
				if cells := tableCells(lines[i]); !separatorRow(cells) {
					rows = append(rows, cells)
				}
			}
			i--
			if len(rows) > 0 {
				out = append(out, SaidBlock{Head: rows[0], Rows: rows[1:]})
			}
		case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* "):
			flush()
			var items []string
			for ; i < len(lines); i++ {
				t := strings.TrimSpace(lines[i])
				if !strings.HasPrefix(t, "- ") && !strings.HasPrefix(t, "* ") {
					break
				}
				items = append(items, strings.TrimSpace(t[2:]))
			}
			i--
			out = append(out, SaidBlock{Items: items})
		default:
			para = append(para, trimmed)
		}
	}
	flush()
	return out
}

// tableCells are the cells of a row of a table: between its bars, with "\|" a bar of the text.
func tableCells(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	if strings.HasSuffix(line, "|") && !strings.HasSuffix(line, `\|`) {
		line = line[:len(line)-1]
	}
	var cells []string
	var cell strings.Builder
	code := false
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c == '\\' && i+1 < len(line) && line[i+1] == '|':
			cell.WriteByte('|')
			i++
		case c == '`':
			code = !code
			cell.WriteByte(c)
		case c == '|' && !code:
			cells = append(cells, strings.TrimSpace(cell.String()))
			cell.Reset()
		default:
			cell.WriteByte(c)
		}
	}
	return append(cells, strings.TrimSpace(cell.String()))
}

// separatorRow reports whether the cells are the row under a table's head ("---", ":--:").
func separatorRow(cells []string) bool {
	for _, c := range cells {
		if strings.Trim(c, "-: ") != "" || !strings.Contains(c, "-") {
			return false
		}
	}
	return len(cells) > 0
}

// spans writes a text with its `code` as code: the rest is escaped, and so is the code.
func spans(text string) template.HTML {
	parts := strings.Split(text, "`")
	if len(parts)%2 == 0 {
		// a backtick with no other: nothing is code
		return template.HTML(template.HTMLEscapeString(text))
	}
	var b strings.Builder
	for i, p := range parts {
		if i%2 == 1 {
			b.WriteString("<code>" + template.HTMLEscapeString(p) + "</code>")
			continue
		}
		b.WriteString(template.HTMLEscapeString(p))
	}
	return template.HTML(b.String())
}
