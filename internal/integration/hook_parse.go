package integration

import "strings"

const batonDisabled = "off"

func shellSegments(command string) [][]string {
	// Read literal words and control operators only; never expand shell text.
	var segments [][]string
	var words []string
	var word strings.Builder
	var quote rune
	started, escaped, comment, redirect := false, false, false, false
	finish := func() {
		if !started {
			return
		}
		if !redirect {
			words = append(words, word.String())
		}
		word.Reset()
		started, redirect = false, false
	}
	chars := []rune(command)
	for index := 0; index < len(chars); index++ {
		char := chars[index]
		if comment && char != '\n' {
			continue
		}
		comment = false
		if escaped {
			if char != '\n' {
				word.WriteRune(char)
				started = true
			}
			escaped = false
			continue
		}
		if char == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
				continue
			}
			word.WriteRune(char)
			continue
		}
		if char == '\'' || char == '"' {
			quote, started = char, true
			continue
		}
		if char == '#' && !started {
			comment = true
			continue
		}
		// Redirection targets (including fd duplication) are never executables.
		if char == '&' && index+1 < len(chars) && chars[index+1] == '>' {
			finish()
			index++
			char = '>'
		}
		if char == '<' || char == '>' {
			if char == '<' && index+1 < len(chars) && chars[index+1] == '<' {
				return nil // Here-doc bodies need a shell parser; leave them opaque.
			}
			if strings.Trim(word.String(), "0123456789") == "" {
				word.Reset()
				started = false
			}
			finish()
			if index+1 < len(chars) && strings.ContainsRune(">&|", chars[index+1]) {
				index++
			}
			redirect = true
			continue
		}
		if strings.ContainsRune(";|&\n()", char) {
			finish()
			segments = append(segments, words)
			words = nil
			continue
		}
		if strings.ContainsRune(" \t\r", char) {
			finish()
			continue
		}
		word.WriteRune(char)
		started = true
	}
	if quote != 0 || escaped {
		return nil
	}
	finish()
	return append(segments, words)
}
