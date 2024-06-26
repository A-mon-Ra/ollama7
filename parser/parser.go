package parser

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type Command struct {
	Name string
	Args string
}

type state int

const (
	stateNil state = iota
	stateName
	stateValue
	stateParameter
	stateMessage
	stateComment
)

var (
	errMissingFrom        = errors.New("no FROM line")
	errInvalidMessageRole = errors.New("message role must be one of \"system\", \"user\", or \"assistant\"")
	errInvalidCommand     = errors.New("command must be one of \"from\", \"license\", \"template\", \"system\", \"adapter\", \"parameter\", or \"message\"")
)

func Format(cmds []Command) string {
	var sb strings.Builder
	for _, cmd := range cmds {
		name := cmd.Name
		args := cmd.Args

		switch cmd.Name {
		case "model":
			name = "from"
			args = cmd.Args
		case "license", "template", "system", "adapter":
			args = quote(args)
		case "message":
			role, message, _ := strings.Cut(cmd.Args, ": ")
			args = role + " " + quote(message)
		default:
			name = "parameter"
			args = cmd.Name + " " + quote(cmd.Args)
		}

		fmt.Fprintln(&sb, strings.ToUpper(name), args)
	}

	return sb.String()
}

func Parse(r io.Reader) (cmds []Command, err error) {
	var cmd Command
	var curr state
	var b bytes.Buffer
	var role string

	br := bufio.NewReader(r)
	for {
		r, _, err := br.ReadRune()
		if errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, err
		}

		next, r, err := parseRuneForState(r, curr)
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, fmt.Errorf("%w: %s", err, b.String())
		} else if err != nil {
			return nil, err
		}

		// process the state transition, some transitions need to be intercepted and redirected
		if next != curr {
			switch curr {
			case stateName:
				if !isValidCommand(b.String()) {
					return nil, errInvalidCommand
				}

				// next state sometimes depends on the current buffer value
				switch s := strings.ToLower(b.String()); s {
				case "from":
					cmd.Name = "model"
				case "parameter":
					// transition to stateParameter which sets command name
					next = stateParameter
				case "message":
					// transition to stateMessage which validates the message role
					next = stateMessage
					fallthrough
				default:
					cmd.Name = s
				}
			case stateParameter:
				cmd.Name = b.String()
			case stateMessage:
				if !isValidMessageRole(b.String()) {
					return nil, errInvalidMessageRole
				}

				role = b.String()
			case stateComment, stateNil:
				// pass
			case stateValue:
				s, ok := unquote(b.String())
				if !ok || isSpace(r) {
					if _, err := b.WriteRune(r); err != nil {
						return nil, err
					}

					continue
				}

				if role != "" {
					s = role + ": " + s
					role = ""
				}

				cmd.Args = s
				cmds = append(cmds, cmd)
			}

			b.Reset()
			curr = next
		}

		if strconv.IsPrint(r) {
			if _, err := b.WriteRune(r); err != nil {
				return nil, err
			}
		}
	}

	// flush the buffer
	switch curr {
	case stateComment, stateNil:
		// pass; nothing to flush
	case stateValue:
		s, ok := unquote(b.String())
		if !ok {
			return nil, io.ErrUnexpectedEOF
		}

		if role != "" {
			s = role + ": " + s
		}

		cmd.Args = s
		cmds = append(cmds, cmd)
	default:
		return nil, io.ErrUnexpectedEOF
	}

	for _, cmd := range cmds {
		if cmd.Name == "model" {
			return cmds, nil
		}
	}

	return nil, errMissingFrom
}

func parseRuneForState(r rune, cs state) (state, rune, error) {
	switch cs {
	case stateNil:
		switch {
		case r == '#':
			return stateComment, 0, nil
		case isSpace(r), isNewline(r):
			return stateNil, 0, nil
		default:
			return stateName, r, nil
		}
	case stateName:
		switch {
		case isAlpha(r):
			return stateName, r, nil
		case isSpace(r):
			return stateValue, 0, nil
		default:
			return stateNil, 0, errInvalidCommand
		}
	case stateValue:
		switch {
		case isNewline(r):
			return stateNil, r, nil
		case isSpace(r):
			return stateNil, r, nil
		default:
			return stateValue, r, nil
		}
	case stateParameter:
		switch {
		case isAlpha(r), isNumber(r), r == '_':
			return stateParameter, r, nil
		case isSpace(r):
			return stateValue, 0, nil
		default:
			return stateNil, 0, io.ErrUnexpectedEOF
		}
	case stateMessage:
		switch {
		case isAlpha(r):
			return stateMessage, r, nil
		case isSpace(r):
			return stateValue, 0, nil
		default:
			return stateNil, 0, io.ErrUnexpectedEOF
		}
	case stateComment:
		switch {
		case isNewline(r):
			return stateNil, 0, nil
		default:
			return stateComment, 0, nil
		}
	default:
		return stateNil, 0, errors.New("")
	}
}

func quote(s string) string {
	if strings.Contains(s, "\n") || strings.HasPrefix(s, " ") || strings.HasSuffix(s, " ") {
		if strings.Contains(s, "\"") {
			return `"""` + s + `"""`
		}

		return `"` + s + `"`
	}

	return s
}

func unquote(s string) (string, bool) {
	if len(s) == 0 {
		return "", false
	}

	// TODO: single quotes
	if len(s) >= 3 && s[:3] == `"""` {
		if len(s) >= 6 && s[len(s)-3:] == `"""` {
			return s[3 : len(s)-3], true
		}

		return "", false
	}

	if len(s) >= 1 && s[0] == '"' {
		if len(s) >= 2 && s[len(s)-1] == '"' {
			return s[1 : len(s)-1], true
		}

		return "", false
	}

	return s, true
}

func isAlpha(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
}

func isNumber(r rune) bool {
	return r >= '0' && r <= '9'
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\t'
}

func isNewline(r rune) bool {
	return r == '\r' || r == '\n'
}

func isValidMessageRole(role string) bool {
	return role == "system" || role == "user" || role == "assistant"
}

func isValidCommand(cmd string) bool {
	switch strings.ToLower(cmd) {
	case "from", "license", "template", "system", "adapter", "parameter", "message":
		return true
	default:
		return false
	}
}
