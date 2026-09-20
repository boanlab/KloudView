// A terminal is a screen, not a transcript.
//
// The console used to append everything the shell sent into one growing
// string. That works for a command and its output and fails for anything that
// draws: less, top, vi and every installer move the cursor, clear the screen
// and redraw in place, so their control sequences arrived as literal text and
// the operator saw garbage where a page should have been.
//
// This is the smallest emulator that renders them: a grid of cells, a cursor,
// a scroll region, an alternate screen, and colour. It carries no dependency,
// which is the point -- the console has no build step and nothing to bundle.

const MAX_SCROLLBACK = 2000;

const ESC = "\x1b";
const BEL = "\x07";

// The xterm palette. 0-15 are named; 16-231 are a 6x6x6 cube; 232-255 grey.
const BASE_COLORS = [
  "#000000", "#cd3131", "#0dbc79", "#e5e510", "#2472c8", "#bc3fbc", "#11a8cd", "#e5e5e5",
  "#666666", "#f14c4c", "#23d18b", "#f5f543", "#3b8eea", "#d670d6", "#29b8db", "#ffffff",
];
const CUBE = [0, 95, 135, 175, 215, 255];

function paletteColor(index) {
  if (index < 16) return BASE_COLORS[index] || null;
  if (index < 232) {
    const n = index - 16;
    return `rgb(${CUBE[Math.floor(n / 36) % 6]},${CUBE[Math.floor(n / 6) % 6]},${CUBE[n % 6]})`;
  }
  if (index < 256) {
    const level = 8 + (index - 232) * 10;
    return `rgb(${level},${level},${level})`;
  }
  return null;
}

const DEFAULT_ATTR = {
  fg: null, bg: null, bold: false, dim: false,
  italic: false, underline: false, inverse: false,
};

function sameAttr(a, b) {
  return (
    a.fg === b.fg && a.bg === b.bg && a.bold === b.bold && a.dim === b.dim &&
    a.italic === b.italic && a.underline === b.underline && a.inverse === b.inverse
  );
}

export function createTerminal(cols = 80, rows = 24) {
  const term = {
    cols: Math.max(2, Math.floor(cols)),
    rows: Math.max(2, Math.floor(rows)),
    cursor: { x: 0, y: 0 },
    saved: null,
    attr: { ...DEFAULT_ATTR },
    scrollTop: 0,
    scrollBottom: Math.max(2, Math.floor(rows)) - 1,
    wrapNext: false,
    autoWrap: true,
    cursorVisible: true,
    alternate: null, // the normal screen, parked while the alternate is shown
    scrollback: [],
    screen: [],
    state: "ground",
    params: "",
    intermediate: "",
    dcs: "",
    // Set by the caller to send the terminal's own answers back. A program
    // that asks where the cursor is and hears nothing waits forever.
    reply: null,
  };

  const blankCell = () => ({ ch: " ", attr: { ...DEFAULT_ATTR } });
  const blankRow = () => Array.from({ length: term.cols }, blankCell);
  term.screen = Array.from({ length: term.rows }, blankRow);

  function remember(row) {
    // Only the normal screen keeps history: what an alternate screen pushes
    // off the top is redrawn by the program, not scrolled back to.
    if (term.alternate) return;
    term.scrollback.push(row);
    if (term.scrollback.length > MAX_SCROLLBACK) term.scrollback.shift();
  }

  function scrollUp(count = 1) {
    for (let i = 0; i < count; i++) {
      const gone = term.screen.splice(term.scrollTop, 1)[0];
      if (term.scrollTop === 0) remember(gone);
      term.screen.splice(term.scrollBottom, 0, blankRow());
    }
  }

  function scrollDown(count = 1) {
    for (let i = 0; i < count; i++) {
      term.screen.splice(term.scrollBottom, 1);
      term.screen.splice(term.scrollTop, 0, blankRow());
    }
  }

  function lineFeed() {
    if (term.cursor.y === term.scrollBottom) scrollUp(1);
    else if (term.cursor.y < term.rows - 1) term.cursor.y += 1;
  }

  function clampCursor() {
    term.cursor.x = Math.min(Math.max(0, term.cursor.x), term.cols - 1);
    term.cursor.y = Math.min(Math.max(0, term.cursor.y), term.rows - 1);
  }

  function put(ch) {
    if (term.wrapNext && term.autoWrap) {
      term.cursor.x = 0;
      lineFeed();
      term.wrapNext = false;
    }
    const row = term.screen[term.cursor.y];
    if (!row) return;
    row[term.cursor.x] = { ch, attr: { ...term.attr } };
    if (term.cursor.x + 1 >= term.cols) term.wrapNext = true;
    else term.cursor.x += 1;
  }

  // ------------------------------------------------------------- sequences
  function numbers(fallback) {
    return term.params.split(";").map((p) => {
      if (p === "") return fallback;
      const value = Number(p);
      return Number.isFinite(value) ? value : fallback;
    });
  }

  function first(fallback = 1) {
    const value = numbers(fallback)[0];
    return Number.isFinite(value) ? value : fallback;
  }

  function eraseInLine(mode) {
    const row = term.screen[term.cursor.y];
    if (!row) return;
    const from = mode === 1 ? 0 : mode === 2 ? 0 : term.cursor.x;
    const to = mode === 1 ? term.cursor.x : term.cols - 1;
    for (let x = from; x <= to; x++) row[x] = blankCell();
  }

  function eraseInDisplay(mode) {
    if (mode === 2 || mode === 3) {
      term.screen = Array.from({ length: term.rows }, blankRow);
      return;
    }
    if (mode === 0) {
      eraseInLine(0);
      for (let y = term.cursor.y + 1; y < term.rows; y++) term.screen[y] = blankRow();
    } else if (mode === 1) {
      eraseInLine(1);
      for (let y = 0; y < term.cursor.y; y++) term.screen[y] = blankRow();
    }
  }

  function applySGR() {
    const codes = term.params === "" ? [0] : numbers(0);
    for (let i = 0; i < codes.length; i++) {
      const code = codes[i];
      if (code === 0) term.attr = { ...DEFAULT_ATTR };
      else if (code === 1) term.attr.bold = true;
      else if (code === 2) term.attr.dim = true;
      else if (code === 3) term.attr.italic = true;
      else if (code === 4) term.attr.underline = true;
      else if (code === 7) term.attr.inverse = true;
      else if (code === 22) { term.attr.bold = false; term.attr.dim = false; }
      else if (code === 23) term.attr.italic = false;
      else if (code === 24) term.attr.underline = false;
      else if (code === 27) term.attr.inverse = false;
      else if (code >= 30 && code <= 37) term.attr.fg = paletteColor(code - 30);
      else if (code === 39) term.attr.fg = null;
      else if (code >= 40 && code <= 47) term.attr.bg = paletteColor(code - 40);
      else if (code === 49) term.attr.bg = null;
      else if (code >= 90 && code <= 97) term.attr.fg = paletteColor(code - 90 + 8);
      else if (code >= 100 && code <= 107) term.attr.bg = paletteColor(code - 100 + 8);
      else if (code === 38 || code === 48) {
        const target = code === 38 ? "fg" : "bg";
        if (codes[i + 1] === 5) { term.attr[target] = paletteColor(codes[i + 2] || 0); i += 2; }
        else if (codes[i + 1] === 2) {
          term.attr[target] = `rgb(${codes[i + 2] || 0},${codes[i + 3] || 0},${codes[i + 4] || 0})`;
          i += 4;
        }
      }
    }
  }

  function setPrivateMode(enabled) {
    for (const code of numbers(0)) {
      if (code === 25) term.cursorVisible = enabled;
      else if (code === 7) term.autoWrap = enabled;
      else if (code === 47 || code === 1047 || code === 1049) {
        // The alternate screen is how a full-screen program borrows the
        // terminal and gives it back: less and vi restore what was underneath
        // when they exit, which is why leaving them does not wipe the session.
        if (enabled && !term.alternate) {
          term.alternate = { screen: term.screen, cursor: { ...term.cursor } };
          term.screen = Array.from({ length: term.rows }, blankRow);
          term.cursor = { x: 0, y: 0 };
        } else if (!enabled && term.alternate) {
          term.screen = term.alternate.screen;
          term.cursor = term.alternate.cursor;
          term.alternate = null;
          clampCursor();
        }
      }
    }
  }

  function answer(data) {
    if (typeof term.reply === "function") term.reply(data);
  }

  function csi(final) {
    const isPrivate = term.params.startsWith("?");
    if (isPrivate) term.params = term.params.slice(1);
    const isSecondary = term.params.startsWith(">");
    if (isSecondary) term.params = term.params.slice(1);
    switch (final) {
      case "A": term.cursor.y -= first(); break;
      case "B": term.cursor.y += first(); break;
      case "C": term.cursor.x += first(); break;
      case "D": term.cursor.x -= first(); break;
      case "E": term.cursor.y += first(); term.cursor.x = 0; break;
      case "F": term.cursor.y -= first(); term.cursor.x = 0; break;
      case "G": case "`": term.cursor.x = first() - 1; break;
      case "d": term.cursor.y = first() - 1; break;
      case "H": case "f": {
        const [row = 1, column = 1] = numbers(1);
        term.cursor.y = (row || 1) - 1;
        term.cursor.x = (column || 1) - 1;
        break;
      }
      case "J": eraseInDisplay(first(0)); break;
      case "K": eraseInLine(first(0)); break;
      case "L": { // insert lines
        for (let i = 0; i < first(); i++) {
          term.screen.splice(term.scrollBottom, 1);
          term.screen.splice(term.cursor.y, 0, blankRow());
        }
        break;
      }
      case "M": { // delete lines
        for (let i = 0; i < first(); i++) {
          term.screen.splice(term.cursor.y, 1);
          term.screen.splice(term.scrollBottom, 0, blankRow());
        }
        break;
      }
      case "P": { // delete characters
        const row = term.screen[term.cursor.y];
        if (row) {
          row.splice(term.cursor.x, first());
          while (row.length < term.cols) row.push(blankCell());
        }
        break;
      }
      case "@": { // insert characters
        const row = term.screen[term.cursor.y];
        if (row) {
          for (let i = 0; i < first(); i++) row.splice(term.cursor.x, 0, blankCell());
          row.length = term.cols;
        }
        break;
      }
      case "X": { // erase characters
        const row = term.screen[term.cursor.y];
        if (row) {
          for (let i = 0; i < first(); i++) {
            if (term.cursor.x + i < term.cols) row[term.cursor.x + i] = blankCell();
          }
        }
        break;
      }
      case "S": scrollUp(first()); break;
      case "T": scrollDown(first()); break;
      case "m": applySGR(); break;
      case "r": { // scroll region
        const [top = 1, bottom = term.rows] = numbers(0);
        term.scrollTop = Math.max(0, (top || 1) - 1);
        term.scrollBottom = Math.min(term.rows - 1, (bottom || term.rows) - 1);
        if (term.scrollBottom <= term.scrollTop) {
          term.scrollTop = 0;
          term.scrollBottom = term.rows - 1;
        }
        term.cursor = { x: 0, y: term.scrollTop };
        break;
      }
      case "n": {
        // A program that centres a dialog or restores a prompt asks the
        // terminal where the cursor is and blocks until it hears back.
        const question = first(0);
        if (question === 6) {
          answer(`\x1b[${isPrivate ? "?" : ""}${term.cursor.y + 1};${term.cursor.x + 1}R`);
        } else if (question === 5) {
          answer("\x1b[0n"); // "the terminal is fine"
        }
        break;
      }
      case "c":
        // Device attributes: what kind of terminal this claims to be. The
        // answer matches what TERM already promises.
        answer(isSecondary ? "\x1b[>0;276;0c" : "\x1b[?1;2c");
        break;
      case "h": if (isPrivate) setPrivateMode(true); break;
      case "l": if (isPrivate) setPrivateMode(false); break;
      case "s": term.saved = { cursor: { ...term.cursor }, attr: { ...term.attr } }; break;
      case "u":
        if (term.saved) { term.cursor = { ...term.saved.cursor }; term.attr = { ...term.saved.attr }; }
        break;
      default: break; // an unknown sequence is consumed, never printed
    }
    term.wrapNext = false;
    clampCursor();
  }

  function escape(ch) {
    switch (ch) {
      case "7": term.saved = { cursor: { ...term.cursor }, attr: { ...term.attr } }; break;
      case "8":
        if (term.saved) { term.cursor = { ...term.saved.cursor }; term.attr = { ...term.saved.attr }; }
        break;
      case "D": lineFeed(); break;
      case "E": term.cursor.x = 0; lineFeed(); break;
      case "M": // reverse index
        if (term.cursor.y === term.scrollTop) scrollDown(1);
        else term.cursor.y -= 1;
        break;
      case "c": term.reset(); break;
      case "Z": answer("\x1b[?1;2c"); break; // the older way to ask what we are
      default: break;
    }
    clampCursor();
  }

  // ------------------------------------------------------------------ feed
  term.write = (text) => {
    for (const ch of String(text)) {
      const code = ch.codePointAt(0);
      switch (term.state) {
        case "escape":
          if (ch === "[") { term.state = "csi"; term.params = ""; term.intermediate = ""; }
          else if (ch === "]") { term.state = "osc"; }
          // A device control string. vim opens with one of these to ask which
          // key sequences the terminal sends (XTGETTCAP); without a parser its
          // payload printed itself across the first line of the file.
          else if (ch === "P") { term.state = "dcs"; term.dcs = ""; }
          else if (ch === "X" || ch === "^" || ch === "_") { term.state = "ignoreString"; }
          else if (ch === "(" || ch === ")" || ch === "*" || ch === "+" || ch === "%" || ch === "#") {
            term.state = "charset";
          } else { escape(ch); term.state = "ground"; }
          continue;
        case "charset":
          term.state = "ground";
          continue;
        case "csi":
          if (code >= 0x30 && code <= 0x3f) { term.params += ch; continue; }
          if (code >= 0x20 && code <= 0x2f) { term.intermediate += ch; continue; }
          csi(ch);
          term.state = "ground";
          continue;
        case "osc":
          // A window title and friends: consumed, never drawn.
          if (ch === BEL) { term.state = "ground"; continue; }
          if (ch === ESC) { term.state = "oscEscape"; continue; }
          continue;
        case "oscEscape":
          term.state = "ground";
          continue;
        case "dcs":
          if (ch === BEL || ch === ESC) {
            // XTGETTCAP asks what a key sends. Saying "I do not know that
            // one" ends the question; silence leaves the program waiting for
            // a timeout before it draws anything.
            if (term.dcs.startsWith("+q")) answer(`${ESC}P0+r${ESC}\\`);
            term.dcs = "";
            term.state = ch === ESC ? "oscEscape" : "ground";
            continue;
          }
          if (term.dcs.length < 256) term.dcs += ch;
          continue;
        case "ignoreString":
          if (ch === BEL) { term.state = "ground"; continue; }
          if (ch === ESC) { term.state = "oscEscape"; continue; }
          continue;
        default:
          break;
      }
      if (ch === ESC) { term.state = "escape"; continue; }
      if (ch === "\n" || ch === "\v" || ch === "\f") { lineFeed(); term.wrapNext = false; continue; }
      if (ch === "\r") { term.cursor.x = 0; term.wrapNext = false; continue; }
      if (ch === "\b") { term.cursor.x = Math.max(0, term.cursor.x - 1); term.wrapNext = false; continue; }
      if (ch === "\t") {
        term.cursor.x = Math.min(term.cols - 1, (Math.floor(term.cursor.x / 8) + 1) * 8);
        term.wrapNext = false;
        continue;
      }
      if (code < 0x20 || code === 0x7f) continue; // other controls are not glyphs
      put(ch);
    }
  };

  term.resize = (nextCols, nextRows) => {
    nextCols = Math.max(2, Math.floor(nextCols));
    nextRows = Math.max(2, Math.floor(nextRows));
    if (nextCols === term.cols && nextRows === term.rows) return false;
    term.cols = nextCols;
    for (const row of term.screen) {
      while (row.length < nextCols) row.push(blankCell());
      row.length = nextCols;
    }
    // Shrinking drops empty rows from the bottom before it gives up a line of
    // output at the top: a smaller window should lose whitespace, not history.
    while (term.screen.length > nextRows) {
      const last = term.screen.length - 1;
      const blank = term.screen[last].every((cell) => cell.ch === " ");
      if (blank && last > term.cursor.y) term.screen.pop();
      else remember(term.screen.shift());
    }
    while (term.screen.length < nextRows) term.screen.push(blankRow());
    term.rows = nextRows;
    term.scrollTop = 0;
    term.scrollBottom = nextRows - 1;
    clampCursor();
    return true;
  };

  term.reset = () => {
    term.screen = Array.from({ length: term.rows }, blankRow);
    term.scrollback = [];
    term.cursor = { x: 0, y: 0 };
    term.saved = null;
    term.attr = { ...DEFAULT_ATTR };
    term.alternate = null;
    term.scrollTop = 0;
    term.scrollBottom = term.rows - 1;
    term.state = "ground";
    term.cursorVisible = true;
    term.autoWrap = true;
    term.wrapNext = false;
  };

  // Every line the session has shown: history above, the live screen below.
  //
  // Except while a full-screen program is running. It asked for the alternate
  // screen because it wants the whole viewport and addresses it by row, so
  // showing the history above it pushes the program's own first line off the
  // top -- vi drew "hello" on row one and the operator saw tildes. A real
  // terminal hides the scrollback for exactly this reason, and gives it back
  // when the program exits.
  term.lines = () =>
    term.alternate ? [...term.screen] : [...term.scrollback, ...term.screen];

  // Plain text, for a recording that is being searched rather than watched.
  term.text = () =>
    term
      .lines()
      .map((row) => row.map((cell) => cell.ch).join("").replace(/\s+$/, ""))
      .join("\n");

  return term;
}

// renderTerminal turns the grid into HTML: one element per run of identical
// attributes, so a plain line stays a single node.
export function renderTerminal(term, escapeHTML) {
  const historyRows = term.scrollback.length;
  return term
    .lines()
    .map((row, index) => {
      const onScreen = index - historyRows;
      const cursorX = term.cursorVisible && onScreen === term.cursor.y ? term.cursor.x : -1;
      // A row is a full-width array, but the blank tail of it is padding, not
      // content: drawing it would put trailing spaces into every copied line
      // and stretch short lines across the pane. A blank cell that carries a
      // colour is content, so only default-attribute blanks are dropped.
      let end = row.length;
      while (end > 0 && end - 1 > cursorX && isPadding(row[end - 1])) end -= 1;
      let html = "";
      let run = "";
      let runAttr = null;
      const flush = () => {
        if (run) html += span(run, runAttr, escapeHTML);
        run = "";
      };
      for (let x = 0; x < end; x++) {
        const cell = row[x];
        if (x === cursorX) {
          flush();
          runAttr = null;
          html += span(cell.ch, { ...cell.attr, inverse: !cell.attr.inverse }, escapeHTML);
          continue;
        }
        if (runAttr && !sameAttr(runAttr, cell.attr)) flush();
        runAttr = cell.attr;
        run += cell.ch;
      }
      flush();
      return `<div class="term-line">${html || "&nbsp;"}</div>`;
    })
    .join("");
}

function isPadding(cell) {
  return cell.ch === " " && sameAttr(cell.attr, DEFAULT_ATTR);
}

function span(text, attr, escapeHTML) {
  const body = escapeHTML(text).replace(/ /g, "&nbsp;");
  if (!attr) return body;
  let fg = attr.fg;
  let bg = attr.bg;
  if (attr.inverse) {
    const swap = fg;
    fg = bg || "var(--terminal-bg)";
    bg = swap || "var(--terminal-fg)";
  }
  const styles = [];
  if (fg) styles.push(`color:${fg}`);
  if (bg) styles.push(`background:${bg}`);
  if (attr.bold) styles.push("font-weight:700");
  if (attr.dim) styles.push("opacity:.7");
  if (attr.italic) styles.push("font-style:italic");
  if (attr.underline) styles.push("text-decoration:underline");
  if (!styles.length) return body;
  return `<span style="${styles.join(";")}">${body}</span>`;
}

// keyBytes turns a browser keydown into what a terminal would have put on the
// wire. A browser gives names ("ArrowUp"); a pty expects the escape sequence
// the key has stood for since VT100, and a program reading the tty has no
// other way to learn a key was pressed.
//
// Returns null for a key that sends nothing -- a bare modifier, or a browser
// shortcut the page should not swallow.
export function keyBytes(event) {
  const { key, ctrlKey, altKey, metaKey } = event;
  if (metaKey) return null; // leave the platform's own shortcuts alone

  const SPECIAL = {
    ArrowUp: "\x1b[A", ArrowDown: "\x1b[B", ArrowRight: "\x1b[C", ArrowLeft: "\x1b[D",
    Home: "\x1b[H", End: "\x1b[F", PageUp: "\x1b[5~", PageDown: "\x1b[6~",
    Insert: "\x1b[2~", Delete: "\x1b[3~",
    Enter: "\r", Tab: "\t", Backspace: "\x7f", Escape: "\x1b",
    F1: "\x1bOP", F2: "\x1bOQ", F3: "\x1bOR", F4: "\x1bOS",
    F5: "\x1b[15~", F6: "\x1b[17~", F7: "\x1b[18~", F8: "\x1b[19~",
    F9: "\x1b[20~", F10: "\x1b[21~", F11: "\x1b[23~", F12: "\x1b[24~",
  };
  if (SPECIAL[key] !== undefined) {
    // Alt holds a key down as ESC then the key, which is how meta bindings
    // reach emacs and readline.
    return altKey ? `\x1b${SPECIAL[key]}` : SPECIAL[key];
  }

  if (ctrlKey) {
    // Ctrl clears the top three bits: Ctrl+A is 0x01, Ctrl+C is 0x03. These
    // are the only way to interrupt, suspend or send end-of-file.
    if (key.length === 1) {
      const upper = key.toUpperCase();
      if (upper >= "A" && upper <= "Z") return String.fromCharCode(upper.charCodeAt(0) - 64);
      const PUNCT = { "@": 0, "[": 27, "\\": 28, "]": 29, "^": 30, "_": 31, " ": 0, "?": 127 };
      if (PUNCT[key] !== undefined) return String.fromCharCode(PUNCT[key]);
    }
    return null; // Ctrl+R, Ctrl+T and friends stay with the browser
  }

  // Anything that produced one character -- including an accented letter or a
  // CJK syllable the IME committed -- goes through as itself.
  if ([...key].length === 1) return altKey ? `\x1b${key}` : key;
  return null;
}
