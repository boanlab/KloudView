package api

import (
	"strings"
	"sync"
	"time"
)

// Screening byte-at-a-time input by reading the echo.
//
// A shell echoes back what is typed at it — including a command recalled from
// history with the up arrow and a path completed with Tab, neither of which
// the operator ever sent as keystrokes. Reassembling the command from the
// keys alone therefore misses exactly the most dangerous case: the recalled
// command is invisible to a keystroke buffer and fully visible on the screen.
//
// So the command being assembled is read off the echo. promptLine follows just
// enough of the output stream to know what sits on the line the cursor is on.
// It is deliberately not a terminal emulator: it keeps one line, because a
// shell prompt is one line, and a line that wraps keeps going on this model
// exactly as the command does.

// promptLine is the echoed line the cursor is currently on, for one session.
type promptLine struct {
	mu         sync.Mutex
	line       []rune
	col        int
	alternate  bool
	lastOutput time.Time
	state      string
	params     []byte
}

func newPromptLine() *promptLine {
	return &promptLine{state: "ground", lastOutput: time.Now()}
}

// feed consumes output on its way to the browser.
func (p *promptLine) feed(data []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastOutput = time.Now()
	for _, ch := range string(data) {
		switch p.state {
		case "escape":
			switch ch {
			case '[':
				p.state = "csi"
				p.params = p.params[:0]
			case ']':
				p.state = "osc"
			case '(', ')', '*', '+', '%', '#':
				p.state = "charset"
			case 'P', 'X', '^', '_':
				// A device control string. vim opens with one to ask what the
				// arrow keys send; its payload is not text and must not end up
				// on the line the deny policy reads.
				p.state = "stringEscape"
			default:
				// A single-character escape. Index and next-line both end the
				// line the same way a newline does.
				if ch == 'D' || ch == 'E' {
					p.reset()
				}
				p.state = "ground"
			}
		case "charset":
			p.state = "ground"
		case "csi":
			if ch >= 0x30 && ch <= 0x3f {
				p.params = append(p.params, byte(ch))
				continue
			}
			if ch >= 0x20 && ch <= 0x2f {
				continue // intermediate bytes carry no argument we use
			}
			p.control(ch)
			p.state = "ground"
		case "osc":
			// A window title. It ends at BEL or at the string terminator, and
			// says nothing about the command line either way.
			if ch == 0x07 {
				p.state = "ground"
			} else if ch == 0x1b {
				p.state = "oscEscape"
			}
		case "stringEscape":
			if ch == 0x07 {
				p.state = "ground"
			} else if ch == 0x1b {
				p.state = "oscEscape"
			}
		case "oscEscape":
			p.state = "ground"
		default:
			p.ground(ch)
		}
	}
}

func (p *promptLine) ground(ch rune) {
	switch ch {
	case 0x1b:
		p.state = "escape"
	case '\n', '\v', '\f':
		p.reset()
	case '\r':
		p.col = 0
	case '\b':
		if p.col > 0 {
			p.col--
		}
	case '\t':
		p.put(' ')
	case 0x07:
		// A bell is not text.
	default:
		if ch < 0x20 || ch == 0x7f {
			return
		}
		p.put(ch)
	}
}

func (p *promptLine) control(final rune) {
	private := len(p.params) > 0 && p.params[0] == '?'
	args := parseParams(p.params)
	first := 1
	if len(args) > 0 && args[0] > 0 {
		first = args[0]
	}
	switch final {
	case 'C': // cursor right
		p.moveTo(p.col + first)
	case 'D': // cursor left
		p.moveTo(p.col - first)
	case 'G', '`': // cursor to column
		p.moveTo(first - 1)
	case 'H', 'f':
		// Absolute positioning is a program drawing a screen, not a shell
		// printing a prompt. Whatever was on the line no longer stands.
		p.reset()
	case 'J': // erase in display
		p.reset()
	case 'K': // erase in line
		mode := 0
		if len(args) > 0 {
			mode = args[0]
		}
		p.eraseInLine(mode)
	case 'P': // delete characters
		p.deleteChars(first)
	case '@': // insert blanks
		p.insertBlanks(first)
	case 'X': // erase characters
		for i := 0; i < first && p.col+i < len(p.line); i++ {
			p.line[p.col+i] = ' '
		}
	case 'h', 'l':
		if !private {
			return
		}
		for _, code := range args {
			// The alternate screen means a full-screen program has the
			// terminal. There is no command line to screen until it gives it
			// back, and the deny policy never had anything to say inside one.
			if code == 1049 || code == 47 || code == 1047 {
				p.alternate = final == 'h'
				p.reset()
			}
		}
	}
}

func (p *promptLine) put(ch rune) {
	p.padTo(p.col)
	if p.col < len(p.line) {
		p.line[p.col] = ch
	} else {
		p.line = append(p.line, ch)
	}
	p.col++
	// A command line is long but not unbounded; the deny policy refuses
	// anything past this length anyway.
	if len(p.line) > 8192 {
		p.reset()
	}
}

func (p *promptLine) padTo(col int) {
	for len(p.line) < col {
		p.line = append(p.line, ' ')
	}
}

func (p *promptLine) moveTo(col int) {
	if col < 0 {
		col = 0
	}
	p.col = col
	p.padTo(col)
}

func (p *promptLine) eraseInLine(mode int) {
	switch mode {
	case 1: // to the cursor
		for i := 0; i < p.col && i < len(p.line); i++ {
			p.line[i] = ' '
		}
	case 2:
		p.line = p.line[:0]
		p.col = 0
	default: // to the end, which is how readline rubs out a recalled command
		if p.col < len(p.line) {
			p.line = p.line[:p.col]
		}
	}
}

func (p *promptLine) deleteChars(count int) {
	if p.col >= len(p.line) {
		return
	}
	end := p.col + count
	if end > len(p.line) {
		end = len(p.line)
	}
	p.line = append(p.line[:p.col], p.line[end:]...)
}

func (p *promptLine) insertBlanks(count int) {
	p.padTo(p.col)
	blanks := make([]rune, count)
	for i := range blanks {
		blanks[i] = ' '
	}
	p.line = append(p.line[:p.col], append(blanks, p.line[p.col:]...)...)
}

func (p *promptLine) reset() {
	p.line = p.line[:0]
	p.col = 0
}

// snapshot is what the line holds now, and whether a full-screen program owns
// the terminal.
func (p *promptLine) snapshot() (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.TrimRight(string(p.line), " "), p.alternate
}

func (p *promptLine) quiet() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return time.Since(p.lastOutput)
}

func parseParams(raw []byte) []int {
	text := string(raw)
	text = strings.TrimPrefix(text, "?")
	text = strings.TrimPrefix(text, ">")
	if text == "" {
		return nil
	}
	values := make([]int, 0, 4)
	for _, part := range strings.Split(text, ";") {
		value := 0
		for _, ch := range part {
			if ch < '0' || ch > '9' {
				value = 0
				break
			}
			value = value*10 + int(ch-'0')
			if value > 1<<20 {
				break
			}
		}
		values = append(values, value)
	}
	return values
}

// commandCandidates is the echoed line reduced to the things that could be a
// command. The deny patterns anchor on the start of a line or on a shell
// separator, and the echo carries the prompt in front — "user@host:/tmp$ rm
// -rf /" would slip past an anchored pattern that "rm -rf /" does not. The
// prompt this session sets ends in "$ " or "# ", so every such boundary is
// tried, along with the whole line for the patterns that anchor on ; & and |.
func commandCandidates(line string) []string {
	candidates := []string{line}
	for _, marker := range []string{"$ ", "# "} {
		rest := line
		for {
			index := strings.Index(rest, marker)
			if index < 0 {
				break
			}
			rest = rest[index+len(marker):]
			candidates = append(candidates, rest)
		}
	}
	return candidates
}
