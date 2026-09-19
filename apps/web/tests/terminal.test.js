import assert from "node:assert/strict";
import test from "node:test";

import { createTerminal, renderTerminal } from "../src/terminal.js";

const ESC = "\x1b";
const escapeHTML = (value) =>
  String(value).replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

function line(term, index) {
  return term.screen[index].map((cell) => cell.ch).join("").replace(/\s+$/, "");
}

test("plain output lands on successive lines", () => {
  const term = createTerminal(20, 5);
  term.write("one\r\ntwo\r\n");
  assert.equal(line(term, 0), "one");
  assert.equal(line(term, 1), "two");
  assert.equal(term.cursor.y, 2);
});

test("a carriage return without a newline overwrites in place", () => {
  const term = createTerminal(20, 5);
  term.write("100%\rdone");
  assert.equal(line(term, 0), "done");
});

test("cursor addressing draws where the program asks", () => {
  const term = createTerminal(20, 5);
  term.write(`${ESC}[3;5Hhere`);
  assert.equal(line(term, 2), "    here");
});

test("erase in display clears the screen a full-screen program takes over", () => {
  const term = createTerminal(10, 3);
  term.write("junk\r\nmore\r\n");
  term.write(`${ESC}[2J${ESC}[HTOP`);
  assert.equal(line(term, 0), "TOP");
  assert.equal(line(term, 1), "");
});

test("erase in line clears from the cursor to the end", () => {
  const term = createTerminal(10, 2);
  term.write("abcdefgh");
  term.write(`${ESC}[1;4H${ESC}[K`);
  assert.equal(line(term, 0), "abc");
});

test("the alternate screen gives the session back when the pager exits", () => {
  const term = createTerminal(20, 3);
  term.write("$ less notes\r\n");
  term.write(`${ESC}[?1049h`);
  term.write(`${ESC}[Hpage one of the file`);
  assert.equal(line(term, 0), "page one of the file");
  term.write(`${ESC}[?1049l`);
  assert.equal(line(term, 0), "$ less notes");
  assert.equal(term.cursor.y, 1);
});

test("a scroll region scrolls only its own rows", () => {
  const term = createTerminal(10, 5);
  term.write(`${ESC}[1;1Hheader`);
  term.write(`${ESC}[2;5r`);        // rows 2..5 scroll, the header stays
  term.write(`${ESC}[5;1Hbottom\n`); // a line feed at the bottom scrolls the region
  assert.equal(line(term, 0), "header");
  assert.equal(line(term, 3), "bottom");
  assert.equal(line(term, 4), "");
});

test("reverse index scrolls the region down", () => {
  const term = createTerminal(10, 3);
  term.write("a\r\nb\r\nc");
  term.write(`${ESC}[1;1H${ESC}M`);
  assert.equal(line(term, 0), "");
  assert.equal(line(term, 1), "a");
});

test("scrolling past the top keeps history, and only for the normal screen", () => {
  const term = createTerminal(10, 2);
  term.write("first\r\nsecond\r\nthird");
  assert.equal(term.scrollback.length, 1);
  assert.match(term.text(), /^first\nsecond\nthird$/);

  term.write(`${ESC}[?1049h`);
  term.write("x\r\ny\r\nz");
  assert.equal(term.scrollback.length, 1, "the alternate screen adds no history");
});

test("colour survives into the rendered HTML and unknown sequences do not", () => {
  const term = createTerminal(20, 2);
  term.write(`${ESC}[31mred${ESC}[0m plain`);
  const html = renderTerminal(term, escapeHTML);
  assert.match(html, /color:#cd3131[^>]*>red/);
  assert.match(html, /plain/);
  assert.doesNotMatch(html, /\x1b/);
});

test("256-colour and true-colour attributes are honoured", () => {
  const term = createTerminal(20, 2);
  term.write(`${ESC}[38;5;196mA${ESC}[38;2;10;20;30mB`);
  const html = renderTerminal(term, escapeHTML);
  assert.match(html, /color:rgb\(255,0,0\)[^>]*>A/);
  assert.match(html, /color:rgb\(10,20,30\)[^>]*>B/);
});

test("markup in the output is escaped, not rendered", () => {
  const term = createTerminal(40, 2);
  term.write("<img src=x onerror=alert(1)>");
  const html = renderTerminal(term, escapeHTML);
  assert.match(html, /&lt;img/);
  assert.doesNotMatch(html, /<img/);
});

test("an OSC title is consumed rather than printed", () => {
  const term = createTerminal(20, 2);
  term.write(`${ESC}]0;ubuntu@host: ~\x07ready`);
  assert.equal(line(term, 0), "ready");
});

test("a sequence split across two writes is still one sequence", () => {
  const term = createTerminal(20, 2);
  term.write(`${ESC}[3`);
  term.write("1mred");
  const html = renderTerminal(term, escapeHTML);
  assert.match(html, /color:#cd3131[^>]*>red/);
});

test("text wraps at the right margin", () => {
  const term = createTerminal(4, 3);
  term.write("abcdef");
  assert.equal(line(term, 0), "abcd");
  assert.equal(line(term, 1), "ef");
});

test("resizing keeps what is on screen and resets the scroll region", () => {
  const term = createTerminal(20, 4);
  term.write("keep me");
  assert.equal(term.resize(10, 3), true);
  assert.equal(term.cols, 10);
  assert.equal(term.rows, 3);
  assert.equal(line(term, 0), "keep me");
  assert.equal(term.scrollBottom, 2);
  assert.equal(term.resize(10, 3), false, "an unchanged size is not a resize");
});

test("insert and delete of lines and characters", () => {
  const term = createTerminal(10, 4);
  term.write("one\r\ntwo\r\nthree");
  term.write(`${ESC}[2;1H${ESC}[L`);   // insert a line above "two"
  assert.equal(line(term, 1), "");
  assert.equal(line(term, 2), "two");
  term.write(`${ESC}[2;1H${ESC}[M`);   // and take it back out
  assert.equal(line(term, 1), "two");
  term.write(`${ESC}[1;1H${ESC}[P`);   // delete one character of "one"
  assert.equal(line(term, 0), "ne");
});

test("the cursor is drawn as an inverted cell and hidden on request", () => {
  const term = createTerminal(5, 2);
  term.write("ab");
  assert.match(renderTerminal(term, escapeHTML), /background:var\(--terminal-fg\)/);
  term.write(`${ESC}[?25l`);
  assert.doesNotMatch(renderTerminal(term, escapeHTML), /background:var\(--terminal-fg\)/);
});

test("a row is drawn to its content, not to the width of the pane", () => {
  const term = createTerminal(40, 3);
  term.write("short\r\n");
  const html = renderTerminal(term, escapeHTML);
  const [firstLine] = html.split("</div>");
  assert.match(firstLine, /short$/, "no padding follows the text");
});

test("a coloured blank is content and survives the trim", () => {
  const term = createTerminal(10, 2);
  term.write(`${ESC}[41m  ${ESC}[0m`);
  assert.match(renderTerminal(term, escapeHTML), /background:#cd3131/);
});

import { keyBytes } from "../src/terminal.js";

const press = (key, mods = {}) => keyBytes({ key, ctrlKey: false, altKey: false, metaKey: false, ...mods });

test("arrow keys become the sequences a program reads them as", () => {
  assert.equal(press("ArrowUp"), `${ESC}[A`);
  assert.equal(press("ArrowDown"), `${ESC}[B`);
  assert.equal(press("ArrowRight"), `${ESC}[C`);
  assert.equal(press("ArrowLeft"), `${ESC}[D`);
});

test("the keys a full-screen editor needs all send something", () => {
  assert.equal(press("Escape"), ESC);
  assert.equal(press("Enter"), "\r");
  assert.equal(press("Tab"), "\t");
  assert.equal(press("Backspace"), "\x7f");
  assert.equal(press("Delete"), `${ESC}[3~`);
  assert.equal(press("PageUp"), `${ESC}[5~`);
  assert.equal(press("Home"), `${ESC}[H`);
  assert.equal(press("F1"), `${ESC}OP`);
});

test("control keys clear the top bits so a session can still be interrupted", () => {
  assert.equal(press("c", { ctrlKey: true }), "\x03");
  assert.equal(press("C", { ctrlKey: true }), "\x03", "shift does not change the control code");
  assert.equal(press("d", { ctrlKey: true }), "\x04");
  assert.equal(press("z", { ctrlKey: true }), "\x1a");
  assert.equal(press("[", { ctrlKey: true }), ESC);
});

test("alt holds a key down as escape-then-key", () => {
  assert.equal(press("b", { altKey: true }), `${ESC}b`);
  assert.equal(press("ArrowLeft", { altKey: true }), `${ESC}${ESC}[D`);
});

test("plain characters go through as themselves, including non-ASCII", () => {
  assert.equal(press("a"), "a");
  assert.equal(press(" "), " ");
  assert.equal(press("한"), "한");
});

test("keys the page must not swallow send nothing", () => {
  assert.equal(press("Shift"), null);
  assert.equal(press("Control"), null);
  assert.equal(press("F5", { metaKey: true }), null, "platform shortcuts stay with the browser");
  assert.equal(press("r", { ctrlKey: true, metaKey: true }), null);
  assert.equal(press("CapsLock"), null);
});

test("the terminal answers the questions a program blocks on", () => {
  const said = [];
  const term = createTerminal(80, 24);
  term.reply = (data) => said.push(data);

  term.write(`${ESC}[5;12H${ESC}[6n`);        // where is the cursor?
  assert.equal(said.at(-1), `${ESC}[5;12R`);

  term.write(`${ESC}[c`);                      // what kind of terminal are you?
  assert.equal(said.at(-1), `${ESC}[?1;2c`);

  term.write(`${ESC}[>c`);                     // and which version?
  assert.equal(said.at(-1), `${ESC}[>0;276;0c`);

  term.write(`${ESC}[5n`);                     // are you all right?
  assert.equal(said.at(-1), `${ESC}[0n`);
});

test("a query answers nothing when no reply channel is attached", () => {
  const term = createTerminal(20, 4);
  assert.doesNotThrow(() => term.write(`${ESC}[6n`));
});

test("a query is consumed, never drawn", () => {
  const term = createTerminal(20, 4);
  term.write(`before${ESC}[6nafter`);
  assert.match(renderTerminal(term, escapeHTML), /beforeafter/);
});

test("a device control string is consumed, not printed", () => {
  // vim opens by asking what the arrow keys send (XTGETTCAP). Without a
  // parser the payload drew itself across the file being edited.
  const term = createTerminal(60, 3);
  term.write(`alpha${ESC}P+q6b75;6b64${ESC}\\beta`);
  assert.equal(
    term.screen[0].map((c) => c.ch).join("").trim(),
    "alphabeta",
  );
});

test("an unknown capability query is answered rather than left hanging", () => {
  const said = [];
  const term = createTerminal(40, 3);
  term.reply = (data) => said.push(data);
  term.write(`${ESC}P+q6b75${ESC}\\`);
  assert.equal(said.at(-1), `${ESC}P0+r${ESC}\\`);
});

test("other string escapes are swallowed whole", () => {
  const term = createTerminal(40, 3);
  term.write(`one${ESC}_application program command${ESC}\\two`);
  assert.equal(term.screen[0].map((c) => c.ch).join("").trim(), "onetwo");
});
