package config

// stripJSONC converts a JSONC document (JSON with // line and /* */ block
// comments plus trailing commas) into plain JSON that encoding/json accepts.
// It is string-aware, so comment markers and commas that appear inside string
// literals are preserved untouched — important for values like "https://…"
// URLs that contain "//".
func stripJSONC(b []byte) []byte {
	return removeTrailingCommas(stripComments(b))
}

// stripComments removes // line comments and /* */ block comments, skipping any
// that occur inside a string literal (with escape handling).
func stripComments(b []byte) []byte {
	out := make([]byte, 0, len(b))
	inString := false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inString {
			out = append(out, c)
			if c == '\\' && i+1 < len(b) {
				out = append(out, b[i+1])
				i++
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
			out = append(out, c)
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			// Line comment: skip to (and keep) the newline.
			i += 2
			for i < len(b) && b[i] != '\n' {
				i++
			}
			if i < len(b) {
				out = append(out, b[i])
			}
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			// Block comment: skip through the closing */.
			i += 2
			for i+1 < len(b) && !(b[i] == '*' && b[i+1] == '/') {
				i++
			}
			i++ // skip '*'; the loop's i++ skips the trailing '/'
		default:
			out = append(out, c)
		}
	}
	return out
}

// removeTrailingCommas drops any comma that is followed (after whitespace) by a
// closing } or ], again skipping string literals. Comments must already be
// stripped before this runs.
func removeTrailingCommas(b []byte) []byte {
	out := make([]byte, 0, len(b))
	inString := false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inString {
			out = append(out, c)
			if c == '\\' && i+1 < len(b) {
				out = append(out, b[i+1])
				i++
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			out = append(out, c)
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(b) && isJSONSpace(b[j]) {
				j++
			}
			if j < len(b) && (b[j] == '}' || b[j] == ']') {
				continue // drop the trailing comma
			}
		}
		out = append(out, c)
	}
	return out
}

func isJSONSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
