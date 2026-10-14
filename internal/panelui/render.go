// Package panelui renders the WebView-hosted account panel that is shared by
// the macOS menu-bar host (cmd/mcs-menubar, WKWebView in an NSPopover) and the
// Windows tray host (cmd/mcs-tray, jchv/go-webview2 in a borderless topmost
// window). Both hosts consume the exact same HTML/CSS/JS output so the UI
// stays in lockstep across platforms.
//
// The JS bridge is feature-detected inside the shared shell(): mac has
// window.webkit.messageHandlers.mcs, Windows has window.mcsAction bound by
// go-webview2's Bind API.
package panelui

import (
	"fmt"
	"html"
	"strings"

	"github.com/miou1107/multi-claude-switcher/core"
	"github.com/miou1107/multi-claude-switcher/core/diagnostics"
)

// ProfileVM is one row in the account-list view.
type ProfileVM struct {
	Folder  string
	Name    string
	Plan    string // subscription label: "Team" | "Max 20×" | "Pro" | "Free" | …
	Current bool

	// SignedIn is false for a profile folder that exists but has no account in
	// it yet. It can still be switched to, which is how the user signs in, but
	// it cannot take part in a sync: sessions are stored per account, so with
	// no account there is no bucket to read from or write to.
	SignedIn bool

	// Convos is how many Code conversations this profile holds for its own
	// account. The sync confirmation quotes it, so the user can see how much is
	// about to move before agreeing to have Claude closed.
	Convos int

	// UUID is the account signed in to this profile, empty when none is. The
	// account list groups by it to spot two profiles holding one account, which
	// is a state the user has to resolve.
	UUID string

	// Usage is the account's 5-hour and weekly limits, nil when unknown. It is
	// filled by AttachUsage, not BuildProfiles, because it arrives later: the
	// list is drawn at once and redrawn when a background read finishes.
	Usage *UsageVM
}

// planPill renders the subscription badge for an account.
func planPill(plan string) string {
	if plan == "" {
		return ""
	}
	switch plan {
	case "Team":
		return `<span class="pill pill-team">🏢 Team</span>`
	case "Free", "API":
		return `<span class="pill pill-personal">` + html.EscapeString(plan) + `</span>`
	default: // Max 20×, Max 5×, Max, Pro
		return `<span class="pill pill-plan">` + html.EscapeString(plan) + `</span>`
	}
}

// versionLink is the version number shown at the foot of a screen. Clicking
// it opens the changelog on GitHub, which is where "what changed in this
// version" is written down.
func versionLink(version string) string {
	return `<button class="aboutlink" title="更新日志" onclick="send('openChangelog','')">v` + html.EscapeString(version) + `</button>`
}

// closeButton is the × in the top-right corner of every screen. It sends the
// same hidePanel action as Esc, which both hosts already handle, so it needs
// no host code: on Windows the panel is parked, on macOS the popover closes.
// It lives in shell() rather than in each renderer so no screen can be added
// without it.
const closeButton = `<button class="closex" title="关闭" aria-label="关闭" onclick="send('hidePanel','')">×</button>`

// shell wraps body content in the shared styled page. Every view lives in the
// same webview — there are no separate windows on either platform.
func shell(body string) string {
	return `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><title>Multi-Claude Switcher</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
html{color-scheme:light}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI","SF Pro Text","PingFang SC","Microsoft YaHei",system-ui,sans-serif;color:#241f38;
  background:linear-gradient(160deg,#efe9fb 0%,#f6eaf2 55%,#f9edf1 100%);padding:16px;-webkit-font-smoothing:antialiased;width:400px;overflow-x:hidden}
.header{display:flex;align-items:center;gap:11px;margin:2px 2px 14px;padding-right:34px}
/* The close button every screen carries in the same corner. Fixed, and above
   the confirm dialog and the progress card, so it is always where the user
   looks for it; it does what Esc does. The header's right padding keeps a long
   title from running under it. */
.closex{position:fixed;top:10px;right:10px;width:28px;height:28px;border-radius:9px;border:none;cursor:pointer;z-index:12;
  background:rgba(255,255,255,.75);color:#6b6580;font-size:18px;line-height:1;display:flex;align-items:center;justify-content:center;
  box-shadow:0 2px 6px rgba(60,40,90,.08)}
.closex:hover{background:#fff;color:#241f38}
.avatar{width:40px;height:40px;border-radius:12px;flex:none;background:linear-gradient(140deg,#8a74f0,#b96cee 55%,#e0607a);
  display:flex;align-items:center;justify-content:center;box-shadow:0 5px 13px rgba(124,108,240,.32)}
.avatar svg{width:22px;height:22px}
.back{width:40px;height:40px;border-radius:12px;flex:none;background:#fff;border:none;cursor:pointer;font-size:18px;color:#7c6cf0;
  display:flex;align-items:center;justify-content:center;box-shadow:0 3px 9px rgba(60,40,90,.08)}
.back:hover{background:#f6f4fb}
.htext h1{font-size:16px;font-weight:800;letter-spacing:-.01em}
.htext p{font-size:12px;color:#6b6580;margin-top:1px}
.cards{display:flex;flex-direction:column;gap:9px}
.card{display:flex;align-items:center;gap:12px;background:#fff;border-radius:14px;padding:12px 14px;
  box-shadow:0 3px 10px rgba(60,40,90,.06);border:2px solid transparent;transition:.13s}
.card.selectable{cursor:pointer}
.card.selectable:hover{box-shadow:0 5px 15px rgba(60,40,90,.13);border-color:#e0dcf3}
.card.selected{border-color:#7c6cf0;background:#faf9ff}
/* The account in use has to stand out from the ones you could switch to at a
   glance: a solid green edge, a green wash and a matching "In use" line, where
   the others are plain white with no subtitle at all. */
.card.current{border-color:#1fa35c;background:linear-gradient(135deg,#e2f7eb,#f2fbf6);box-shadow:0 4px 14px rgba(26,138,79,.18)}
.card.current .name{color:#11613a}
.sub-current{font-size:11.5px;font-weight:700;color:#1a8a4f;margin-top:1px}
.card.ghost{opacity:.55}
.chev{width:24px;height:24px;flex:none;border-radius:8px;background:#f1eef9;color:#7c6cf0;font-size:14px;display:flex;align-items:center;justify-content:center}
.dotcur{width:9px;height:9px;flex:none;border-radius:50%;background:#1a8a4f;margin:0 7px 0 3px;box-shadow:0 0 0 3px #d6f5e3}
.chk{appearance:none;-webkit-appearance:none;width:21px;height:21px;flex:none;border-radius:6px;border:2px solid #cdc8e0;background:#fff;position:relative}
.selected .chk{background:#7c6cf0;border-color:#7c6cf0}
.chk:checked::after{content:"";position:absolute;left:6px;top:2px;width:6px;height:11px;border:solid #fff;border-width:0 2.5px 2.5px 0;transform:rotate(45deg)}
.body{flex:1;min-width:0}
.row1{display:flex;align-items:center;gap:7px}
.name{font-size:14px;font-weight:700;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.sub{font-size:11.5px;color:#8b8598;margin-top:1px}
.meta{font-size:11px;color:#8b8598;margin-top:5px;display:flex;align-items:center;gap:5px;flex-wrap:wrap}
.chip{font-family:ui-monospace,SFMono-Regular,Consolas,monospace;font-size:10.5px;background:#f1eef9;color:#6b6580;padding:2px 6px;border-radius:5px}
.dot{opacity:.5}
.pill{font-size:10.5px;font-weight:700;padding:2px 8px;border-radius:999px;white-space:nowrap}
.pill-team{background:#d6f5e3;color:#1a8a4f}
.pill-personal{background:#eceaf3;color:#6b6580}
.pill-plan{background:#ece8fb;color:#6a4fd0}
.usage{margin-top:6px;display:flex;flex-direction:column;gap:3px}
.urow{display:flex;align-items:center;gap:6px;font-size:10.5px;color:#8b8598}
.ulabel{width:38px;flex:none;font-weight:700;color:#6b6580}
.ubar{width:72px;flex:none;height:6px;border-radius:999px;background:#eceaf3;overflow:hidden}
.ubar i{display:block;height:100%;border-radius:999px}
.u-ok{background:#1fa35c}
.u-mid{background:#e0a020}
.u-hi{background:#c0392b}
.upct{width:32px;flex:none;text-align:right;font-weight:700;color:#3d3750}
.ureset{white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.unote{font-size:10.5px;color:#8b8598;line-height:1.35}
.addcard{display:flex;align-items:center;justify-content:center;gap:7px;background:transparent;border:2px dashed #cdc8e0;border-radius:14px;padding:13px 14px;cursor:pointer;font:inherit;font-size:13px;font-weight:800;color:#6b6580;width:100%}
.addcard:hover{border-color:#7c6cf0;color:#7c6cf0;background:#faf9ff}
.note-bad{margin-top:5px;display:inline-block;font-size:10.5px;font-weight:700;background:#fde4e4;color:#c0392b;padding:2px 8px;border-radius:999px}
.dup{background:#fde4e4;border-radius:12px;padding:11px 13px;margin-bottom:11px;display:flex;align-items:center;gap:10px}
.dup .dt{flex:1;font-size:12px;color:#a32d2d;line-height:1.45}
.dup-pill{font-size:10.5px;font-weight:700;padding:2px 8px;border-radius:999px;background:#fde4e4;color:#a32d2d;white-space:nowrap}
.btn-sm{font:inherit;font-size:12px;font-weight:700;border:none;cursor:pointer;border-radius:9px;padding:7px 12px;background:linear-gradient(135deg,#7c6cf0,#9b6bff);color:#fff;flex:none}
.btn-sm:hover{filter:brightness(1.05)}
.note-todo{margin-top:5px;display:inline-block;font-size:10.5px;font-weight:700;background:#e6eefc;color:#2b62c9;padding:2px 8px;border-radius:999px;white-space:normal}
.empty{color:#8b8598;font-size:13px;text-align:center;padding:18px 8px}
.footer{display:flex;gap:9px;margin-top:14px}
.btn{flex:1;font:inherit;font-weight:700;font-size:13px;border:none;cursor:pointer;border-radius:11px;padding:10px;transition:.13s}
.btn-light{background:#fff;color:#514b66;box-shadow:0 3px 9px rgba(60,40,90,.08)}
.btn-light:hover{background:#f6f4fb}
.btn-primary{background:linear-gradient(135deg,#7c6cf0,#9b6bff);color:#fff;box-shadow:0 4px 12px rgba(124,108,240,.4)}
.btn-primary:hover{filter:brightness(1.05)}
/* The confirm button of a destructive dialog, and only that one. Every other
   dialog keeps btn-primary: red on all of them would read as "this is the
   confirm button" rather than "this one takes something away". */
.btn-danger{background:linear-gradient(135deg,#d5566d,#c0392b);color:#fff;box-shadow:0 4px 12px rgba(192,57,43,.35)}
.btn-danger:hover{filter:brightness(1.05)}
.btn-quit{flex:none;padding:10px 15px;background:#fff;color:#b0455f;box-shadow:0 3px 9px rgba(60,40,90,.08)}
.btn-quit:hover{background:#fdf2f5}
.gear{flex:none;padding:10px 14px;background:#fff;color:#514b66;box-shadow:0 3px 9px rgba(60,40,90,.08);font-size:15px}
.gear:hover{background:#f6f4fb}
.slist{display:flex;flex-direction:column;gap:9px}
.srow{display:flex;align-items:center;gap:12px;background:#fff;border-radius:14px;padding:13px 15px;box-shadow:0 3px 10px rgba(60,40,90,.06)}
.slabel{flex:1;min-width:0}
.slabel .t{font-size:14px;font-weight:600}
.slabel .s{font-size:11.5px;color:#8b8598;margin-top:1px}
.sbtn{width:100%;text-align:left;background:#fff;border:none;border-radius:14px;padding:13px 15px;font:inherit;font-size:14px;font-weight:600;cursor:pointer;box-shadow:0 3px 10px rgba(60,40,90,.06);color:#241f38}
.sbtn:hover{background:#faf9ff}
.sbtn:disabled{opacity:.5;cursor:default;color:#8b8598}
.sbtn:disabled:hover{background:#fff}
/* Red text inside a red border, so the one button on the account screen that
   takes something away does not read as another row of settings. It sits below
   a rule and away from Save, which is the other half of the delete-button rule
   this follows. */
.sbtn.danger{color:#b0455f;border:1.5px solid #e0a3b1}
.sbtn.danger:hover{background:#fdf2f5}
.sbtn.danger:disabled{border-color:#ded9ec}
.toggle{width:44px;height:26px;border-radius:999px;background:#d5d0e6;position:relative;flex:none;transition:.15s;cursor:pointer}
.toggle.on{background:#7c6cf0}
.toggle::after{content:"";position:absolute;top:3px;left:3px;width:20px;height:20px;border-radius:50%;background:#fff;transition:.15s;box-shadow:0 1px 3px rgba(0,0,0,.2)}
.toggle.on::after{left:21px}
/* The footer carries the version and the two actions nobody performs daily,
   drawn quietly because neither is what somebody opened this screen to do.
   They are still BUTTONS: an <a> with no href is not a tab stop and does not
   answer Enter, and the bug report screen is reached only from here now, so
   anchors would have put it beyond anyone not using a mouse. */
.about{display:flex;gap:12px;justify-content:center;align-items:center;flex-wrap:wrap;font-size:11.5px;color:#8b8598;margin-top:14px}
.about .sep{opacity:.35}
.aboutlink{color:#6b6480;background:none;border:none;padding:0;font:inherit;font-size:11.5px;cursor:pointer}
.aboutlink:hover{text-decoration:underline}
.aboutlink:disabled{opacity:.45;cursor:default;text-decoration:none}
/* A chevron means the row opens another screen, mirroring the back button's
   own arrow. Rows that act rather than navigate carry nothing.
   NOT .chev: that name was already taken by the account list's switch chip and
   the sync screen's direction chip, and adding to it repainted both from lilac
   to grey and handed this one a 24px chip background it never wanted. */
.navchev{float:right;color:#b3aec0;font-weight:400}
.status{background:#e3f3e8;color:#1a7a3d;font-size:12.5px;font-weight:600;padding:9px 13px;border-radius:11px;margin-bottom:11px;text-align:center}
.rninput{width:100%;font:inherit;font-size:15px;padding:13px 15px;border:2px solid #e0dcf3;border-radius:14px;background:#fff;color:#241f38;outline:none}
.rninput:focus{border-color:#7c6cf0}
/* The row menu (Rename / Remove), anchored to its chevron. Absolutely
   positioned off the wrap, not the card, so it floats above whatever sits
   below it in the list rather than pushing those rows down. */
.rowmenu-wrap{position:relative;flex:none}
/* Bordered rather than a bare glyph. Unboxed and half-faded, it read as
   decoration and went unnoticed: the edges are what say "you can press this".
   The wrench is drawn inline rather than set as a character, because every
   wrench in Unicode resolves to the colour emoji font on macOS, which would
   put one full-colour icon among a panel of flat monochrome ones. */
.chevbtn{width:28px;height:28px;flex:none;border:1px solid #ded7ee;border-radius:8px;background:#fff;color:#5f5878;cursor:pointer;display:flex;align-items:center;justify-content:center;padding:0}
.chevbtn:hover,.chevbtn.open{background:#f1eef9;border-color:#c9bff0;color:#7c6cf0}
.chevbtn svg{width:15px;height:15px;display:block}
.rowmenu{display:none;position:absolute;right:0;top:calc(100% + 4px);min-width:130px;background:#fff;border-radius:10px;box-shadow:0 8px 24px rgba(60,40,90,.2);overflow:hidden;z-index:5}
.rowmenu.open{display:block}
/* The panel is a fixed-height popover with nothing to scroll, so a menu near
   the bottom of the list has to open upward instead of running off the
   window with no way to reach it. .up is toggled from the button's actual
   position at click time (see toggleRowMenu in the script below), not
   guessed here from a row's index. */
.rowmenu.up{top:auto;bottom:calc(100% + 4px)}
.rowmenu button{display:block;width:100%;text-align:left;background:none;border:none;font:inherit;font-size:13px;font-weight:600;padding:10px 13px;cursor:pointer;color:#241f38}
.rowmenu button:hover{background:#f6f4fb}
.rowmenu button.danger{color:#b0455f}
.rowmenu button.danger:hover{background:#fdf2f5}
/* Renaming happens in the row: .viewrow (name + sub-copy) and .editrow (the
   input and its two controls) are both always in the markup, and the card's
   own .renaming class flips which one is visible. Nothing is built or torn
   down by JS, so there is no separate "does the input exist yet" state to
   get wrong. */
.editrow{display:none;align-items:center;gap:6px}
.card.renaming .viewrow{display:none}
.card.renaming .editrow{display:flex}
.rnrow{flex:1;min-width:0;font:inherit;font-size:13px;padding:7px 10px;border:2px solid #e0dcf3;border-radius:9px;background:#fff;color:#241f38;outline:none}
.rnrow:focus{border-color:#7c6cf0}
.rncancel{flex:none;background:#fff;color:#514b66;border:none;border-radius:8px;padding:7px 9px;font:inherit;font-size:12px;font-weight:700;cursor:pointer}
.rncancel:hover{background:#f6f4fb}
.rnsave{flex:none;background:linear-gradient(135deg,#7c6cf0,#9b6bff);color:#fff;border:none;border-radius:8px;padding:7px 11px;font:inherit;font-size:12px;font-weight:700;cursor:pointer}
.rnsave:hover{filter:brightness(1.05)}
.hint{font-size:12px;color:#6b6580;line-height:1.5;margin-top:11px}
.hintw{background:#fff6e0;color:#854f0b;font-size:12px;line-height:1.5;padding:9px 12px;border-radius:11px;margin-top:10px}
.hintw .noteline+.noteline{margin-top:7px}
.dbgnote{background:#e9f5ee;color:#1a7a3d;font-size:11.5px;line-height:1.5;padding:9px 12px;border-radius:11px;margin-bottom:9px}
.dbgbox{background:#fff;border-radius:12px;padding:11px 12px;font-family:ui-monospace,SFMono-Regular,Consolas,monospace;font-size:10.5px;line-height:1.65;color:#514b66;max-height:210px;overflow:auto;white-space:pre-wrap;word-break:break-word}
.dbgarea{width:100%;height:60px;font:inherit;font-size:12px;padding:10px 12px;border:2px solid #e0dcf3;border-radius:12px;background:#fff;color:#241f38;outline:none;resize:none}
.dbgarea:focus{border-color:#7c6cf0}
.errbox{background:#fde4e4;color:#a32d2d;font-size:12px;font-weight:700;padding:9px 12px;border-radius:11px;margin-bottom:11px}
.modal-bg{position:fixed;inset:0;background:rgba(30,20,50,.32);display:none;align-items:center;justify-content:center;z-index:10;padding:20px}
.modal-bg.on{display:flex}
.modal{background:#fff;border-radius:16px;padding:20px 20px 16px;width:100%;max-width:340px;box-shadow:0 12px 40px rgba(30,20,50,.28)}
.modal h2{font-size:15px;font-weight:800;margin-bottom:8px;letter-spacing:-.01em}
.modal p{font-size:12.5px;color:#6b6580;line-height:1.5;margin-bottom:14px}
.modal .warn{background:#fff6e0;color:#854f0b;font-size:12px;line-height:1.45;padding:8px 11px;border-radius:10px;margin-bottom:14px}
.modal .row{display:flex;gap:9px}
.modal .btn{flex:1}
/* The switch progress card. Above .modal-bg so the confirmation it replaces
   cannot show through, and centred in the same place so pressing Switch turns
   the question into the progress without the user's eyes moving. */
.prog-bg{position:fixed;inset:0;background:rgba(30,20,50,.32);display:flex;align-items:center;justify-content:center;z-index:11;padding:20px}
.prog{background:#fff;border-radius:16px;padding:26px 20px 22px;width:100%;max-width:340px;box-shadow:0 12px 40px rgba(30,20,50,.28);text-align:center}
.prog h2{font-size:15px;font-weight:800;margin:0 0 6px;letter-spacing:-.01em}
.prog p{font-size:12.5px;color:#6b6580;line-height:1.5;margin:0}
.prog .btn{width:100%;margin-top:15px}
.prog-err{color:#a32d2d;font-weight:600;word-break:break-word}
.prog-warn{background:#fff6e0;color:#854f0b;font-size:12px;line-height:1.45;text-align:left;padding:9px 11px;border-radius:10px;margin-top:12px;word-break:break-word}
.prog-spin{width:34px;height:34px;border:3px solid #e6e3f0;border-top-color:#7c6cf0;border-radius:50%;margin:0 auto 15px;animation:progspin .8s linear infinite}
@keyframes progspin{to{transform:rotate(360deg)}}
.prog-mark{width:38px;height:38px;border-radius:50%;color:#fff;font-size:19px;font-weight:800;line-height:38px;margin:0 auto 13px}
.prog-mark.ok{background:#1a7a3d}
.prog-mark.bad{background:#c23b3b}
</style></head><body>` + closeButton + body + `
<div class="modal-bg" id="mcsModal" onclick="if(event.target===this) closeConfirm()">
  <div class="modal" role="dialog" aria-modal="true" aria-labelledby="mcsModalTitle" aria-describedby="mcsModalBody">
    <h2 id="mcsModalTitle">关闭 Claude？</h2>
    <p id="mcsModalBody"></p>
    <div class="warn">Claude 里没保存的内容会被中断。</div>
    <div class="row">
      <button class="btn btn-light" id="mcsModalCancel" onclick="closeConfirm()">取消</button>
      <button class="btn btn-primary" id="mcsModalOk" onclick="okConfirm()">继续</button>
    </div>
  </div>
</div>
<script>
  // Bridge: the host binds a Go function as window.mcsAction via go-webview2's
  // Bind API.
  function send(a, arg){
    window.mcsAction(a, arg || '');
  }
  // The window is only as tall as what is on it. The host used to be a fixed
  // 540px, which left a band of empty space under every short screen. This
  // reports the content's height, and the window's own height for scale, each
  // time the content changes size; the host resizes to fit, between a floor
  // that leaves room for the confirm dialog and progress card and the old
  // 540px ceiling, above which the page scrolls as before.
  var lastFit = '';
  function fitHeight(){
    var arg = Math.ceil(document.body.getBoundingClientRect().bottom) + ',' + window.innerHeight;
    if (arg === lastFit) return;
    lastFit = arg;
    send('fitHeight', arg);
  }
  if (window.ResizeObserver) { new ResizeObserver(fitHeight).observe(document.body); }
  window.addEventListener('load', fitHeight);
  function toggleCard(el){ el.classList.toggle('selected'); var c=el.querySelector('.chk'); if(c) c.checked=el.classList.contains('selected'); }
  function confirmManaged(){
    var picked=[];
    document.querySelectorAll('.card.selectable.selected').forEach(function(c){ if(c.dataset.folder) picked.push(c.dataset.folder); });
    send('confirmManaged', JSON.stringify(picked));
  }
  // Row menu: the wrench on an account row opens that row's own menu
  // alone. Only one is ever open — opening another, or a click anywhere else
  // on the page, closes it via closeAllRowMenus, and Escape does too (see the
  // keydown handler below, where that check runs before anything else the
  // panel already did on Escape).
  function closeAllRowMenus(){
    document.querySelectorAll('.rowmenu.open').forEach(function(m){ m.classList.remove('open','up'); });
    document.querySelectorAll('.chevbtn.open').forEach(function(b){
      // The button's icon is an inline SVG, so the open state is carried by the
      // class alone: writing textContent here would delete the icon.
      b.classList.remove('open'); b.setAttribute('aria-expanded','false');
    });
  }
  // Opens downward by default. The panel is a fixed-height popover with
  // nothing to scroll, so a row near the bottom has to open its menu upward
  // instead, or the menu is unreachable. That is decided here, from the
  // button's real position when it is clicked, not guessed in Go from the
  // row's index — a guess in Go would have to know the popover's actual
  // rendered height, which depends on how many rows and banners are on
  // screen, not on anything RenderList itself controls.
  function toggleRowMenu(btn){
    cancelAllRenames();
    var menu = btn.nextElementSibling;
    var willOpen = !menu.classList.contains('open');
    closeAllRowMenus();
    if (!willOpen) return;
    var r = btn.getBoundingClientRect();
    menu.classList.toggle('up', r.bottom > window.innerHeight - 90);
    menu.classList.add('open');
    btn.classList.add('open');
    btn.setAttribute('aria-expanded','true');
  }
  // Capture phase, and it swallows the click. "Click somewhere else to close it"
  // is how everyone dismisses a menu, and on the bubble phase the card's own
  // handler had already run by the time this fired: dismissing row A's menu by
  // clicking row B also raised "Switch to B?", which is one Enter away from
  // closing the user's Claude. Only ever swallows while a menu is actually open,
  // so an ordinary click on a row is untouched.
  document.addEventListener('click', function(e){
    if (e.target.closest('.rowmenu-wrap')) return;
    if (!document.querySelector('.rowmenu.open')) return;
    closeAllRowMenus();
    e.stopPropagation();
    e.preventDefault();
  }, true);
  // Renaming happens in the row itself: Rename swaps its .viewrow for the
  // .editrow already sitting in the markup (see the .card.renaming CSS
  // rules), rather than building or tearing down any DOM. Saving still sends
  // the same renameSave action with the same [folder, value] payload the old
  // Account settings screen sent, so the Go side reading it does not change.
  function cancelAllRenames(){
    document.querySelectorAll('.card.renaming').forEach(function(c){ c.classList.remove('renaming'); });
  }
  function startRename(el){
    closeAllRowMenus();
    // One row at a time. Two open editors is a state nothing else in the panel
    // handles, and the second one silently discards the first.
    cancelAllRenames();
    var card = el.closest('.card');
    var input = card.querySelector('.rnrow');
    input.value = card.dataset.name;
    card.classList.add('renaming');
    input.focus();
    input.select();
    // Tell Go an editor is open. A reload replaces the whole document, so any
    // background action finishing mid-edit used to wipe what was typed with no
    // warning; Go holds the list still while this is set.
    send('renameOpen', '1');
  }
  function cancelRename(el){
    el.closest('.card').classList.remove('renaming');
    send('renameOpen', '0');
  }
  function rowRenameSave(el){
    var card = el.closest('.card');
    var v = card.querySelector('.rnrow').value.trim();
    card.classList.remove('renaming');
    send('renameSave', JSON.stringify([card.dataset.folder, v]));
  }
  // Enter saves, Escape cancels, both without waiting on a round trip to Go:
  // stopPropagation keeps the document keydown handler below from also
  // treating this Escape as "focus is in some INPUT, back out to the list".
  function rowRenameKey(e, input){
    if (e.key === 'Enter') { e.preventDefault(); rowRenameSave(input); }
    else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); cancelRename(input); }
  }
  function createProfileSave(btn){
    var v=document.getElementById('np').value.trim();
    send('createProfile', JSON.stringify([v, btn.dataset.uuid||'']));
  }
  function toggleMergePick(el){
    var cards=el.parentNode.querySelectorAll('.card.selectable');
    for(var i=0;i<cards.length;i++){
      var on=cards[i]===el;
      cards[i].classList.toggle('selected',on);
      var c=cards[i].querySelector('.chk'); if(c) c.checked=on;
    }
  }
  function mergeConfirm(){
    var sel=document.querySelector('.card.selectable.selected');
    var all=document.querySelectorAll('.card.selectable');
    if(!sel||all.length!==2) return;
    var other=all[0]===sel?all[1]:all[0];
    send('mergeConfirm', sel.dataset.folder+'|'+other.dataset.folder);
  }

  // Every action that closes the user's Claude goes through askConfirm. Nothing
  // may call send('switch') or send('sync') directly: closing an app somebody is
  // working in is not a single-click operation, and a warning that one code path
  // can skip is not a warning.
  //
  // kind is 'destructive' for a dialog that takes something away, and anything
  // else (omitted, in practice) for the rest. It only paints the confirm button:
  // switching, syncing and reporting a problem all close Claude or publish
  // something, but none of them removes an account, and a red button on every
  // dialog would stop meaning anything.
  //
  // action==='' makes this an informational dialog: there is nothing to confirm
  // (Claude being open on the account is not something "Got it" can fix), so
  // Cancel is hidden and the one button just closes the dialog. This is the only
  // way an informational dialog is built; there is no second modal.
  var _pending=null;
  function askConfirm(action, arg, title, body, okLabel, warn, kind){
    _pending={a:action, arg:arg};
    document.getElementById('mcsModalTitle').textContent=title;
    document.getElementById('mcsModalBody').textContent=body;
    // warn omitted (arguments.length<6) keeps the shared line every dialog used
    // to carry unconditionally. An explicitly empty string hides the block
    // outright: plain "warn || default" cannot tell those two apart, since ''
    // is falsy the same as undefined, which is exactly what askRemove now
    // needs: its confirmation says everything once, in the body, with no
    // second block repeating a warning underneath.
    var warnText = arguments.length >= 6 ? warn : 'Claude 里没保存的内容会被中断。';
    var warnEl = document.querySelector('#mcsModal .warn');
    warnEl.textContent = warnText;
    warnEl.style.display = warnText ? '' : 'none';
    var ok=document.getElementById('mcsModalOk');
    // Set both ways round: the dialog is reused, so a plain confirm opened after
    // a removal must not inherit the red button.
    ok.classList.toggle('btn-danger', kind==='destructive');
    ok.classList.toggle('btn-primary', kind!=='destructive');
    document.getElementById('mcsModalOk').textContent=okLabel;
    // An informational dialog has nothing to cancel out of.
    document.getElementById('mcsModalCancel').style.display = action === '' ? 'none' : '';
    document.getElementById('mcsModal').classList.add('on');
    // Cancel takes the focus, not Continue, so Enter on an unread dialog cannot
    // close somebody's app. The informational dialog has no Cancel to give it
    // to, so it focuses OK instead: that button is the only one on screen, and
    // it only closes the dialog, so focusing it is safe. Leaving focus on
    // whatever control behind the overlay opened it would let Tab walk the
    // screen underneath while the dialog is up, and Enter would re-open the
    // same dialog instead of doing nothing.
    (action === '' ? ok : document.getElementById('mcsModalCancel')).focus();
  }
  function askSwitch(folder, name){
    askConfirm('switch', folder, '切换到 '+name+'？',
      'Claude 会关闭，然后以 '+name+' 的身份重新打开。', '切换');
  }
  function askReport(){
    askConfirm('reportProblem', document.getElementById('dbgc').value,
      '打开 GitHub issue？',
      '上面的报告和你的说明会复制到剪贴板，浏览器会在 MCS 仓库打开一个新 issue。粘贴进去后，提交前仍然可以修改。',
      '复制并打开',
      'GitHub issue 是公开的。复制的内容就是这个对话框后面屏幕上显示的内容，邮箱、账号 ID、用户名和主目录都已经换成了代称。');
  }
  // The folder and name travel as data-* and are read back with dataset, never
  // interpolated into the inline handler: a name with an apostrophe would
  // otherwise break the parse (the v0.9.1 bug).
  function askRemove(el){
    // Close the row's own menu before putting a dialog over it. The menu item's
    // stopPropagation keeps the document click handler from doing it, so without
    // this the menu is merely hidden behind the scrim and still .open, and the
    // Escape chain (which closes an open row menu first, on purpose) spends the
    // user's first Escape on something invisible while the dialog sits there
    // looking like it ignored the key.
    closeAllRowMenus();
    // The button is always live now: a disabled control cannot explain
    // itself. If Claude is open on this account, remove has nothing to confirm,
    // so it opens an informational dialog instead of the destructive one, and
    // "Got it" just closes it.
    if (el.dataset.current === '1') {
      askConfirm('', '', 'Claude 正在使用 '+el.dataset.name,
        '先切换到别的账号，然后才能移除它。', '知道了', '');
      return;
    }
    var n = parseInt(el.dataset.convos, 10) || 0;
    // Zero gets its own wording rather than falling through to "0
    // conversations kept": a freshly created, never-signed-in slot is the
    // profile most likely to be the one somebody removes, so this is not a
    // rare edge. For one-or-more, the kept-not-deleted fact and the sign-in-
    // again fact are two separate sentences on purpose: signing in again
    // starts a new copy of the account, and does not bring the old
    // conversations into it, so a single sentence joining the two ("kept, and
    // you can add it back") would read as a promise this does not keep.
    // RenderRemoved (the screen after a removal) already gets this right; this
    // matches its honesty, not its wording, since this dialog asks before the
    // fact and that one reports after it.
    var what = n === 0 ? '它会从列表中移除。不会删除任何东西，以后随时可以重新登录。'
      : '它会从列表中移除。它的 ' + n + ' 个对话会保留，不会删除。重新登录这个账号会建立一份新的副本，不包含这些对话。';
    // No warn block: the body above already says everything the warning line
    // used to repeat, so an empty string here hides it rather than falling
    // back to the shared "Anything unsaved…" line, which does not apply since
    // this dialog is not about to close Claude.
    askConfirm('removeProfile', el.dataset.folder, '移除 '+el.dataset.name+'？',
      what, '移除', '', 'destructive');
  }
  // The folders arrive via data-* and are joined here, never interpolated into the
  // inline handler: a folder with an apostrophe would otherwise break the parse.
  function mergePair(a, b){ send('showMerge', a+'|'+b); }
  function askSync(from, to, fromName, toName, convos){
    var n = parseInt(convos, 10) || 0;
    askConfirm('sync', from+'|'+to, '同步到 '+toName+'？',
      'Claude 会关闭，从 '+fromName+' 复制 '+n+' 个对话过去，然后 Claude 会回到你原来的账号重新打开。', '同步');
  }
  function closeConfirm(){ _pending=null; document.getElementById('mcsModal').classList.remove('on'); }
  // An informational dialog (action==='') has nothing to send: it exists only
  // to be read and closed.
  function okConfirm(){ var p=_pending; closeConfirm(); if(p && p.a) send(p.a, p.arg); }
  // Enter is intentionally NOT hijacked: browsers activate the focused button on Enter,
  // so tabbing to Cancel and pressing Enter cancels; hijacking it would silently confirm.
  document.addEventListener('keydown', function(e){
    if(e.key!=='Escape') return;
    // A row's own menu (Rename/Remove) closes before anything else on this
    // list: it is a small, row-anchored overlay, not a screen, and closing it
    // is the whole meaning of Esc while it is open. The row's inline rename
    // input has its own Escape handler (rowRenameKey) that stops propagation,
    // so it never reaches this far down the chain in the first place.
    if(document.querySelector('.rowmenu.open')) { closeAllRowMenus(); return; }
    if(document.getElementById('mcsModal').classList.contains('on')) { closeConfirm(); return; }
    // An open inline rename outranks the rest of this chain. rowRenameKey
    // handles Escape while focus is in the input itself, but focus can leave it
    // (Tab reaches the row's own Cancel and Save), and from there Escape used to
    // fall all the way through to hidePanel and shut the panel on a half-typed
    // name.
    var editing = document.querySelector('.card.renaming');
    if(editing) { editing.classList.remove('renaming'); send('renameOpen','0'); return; }
    // Inside a text input (the new-profile name field), Esc backs out to the
    // list instead of killing the panel; hiding on Windows would discard the
    // typed name.
    var ae=document.activeElement;
    // The Debug comment box is the one exception: showList jumps past
    // Settings, which the back button does not, and, same as pressing that
    // back button used to, discarded whatever the user had typed, since it
    // was never sent to Go until Copy or Report a problem. Sending it as the
    // arg here mirrors the back button's fix: showSettings on the Go side
    // saves it before switching the view away.
    if(ae && ae.id==='dbgc') { send('showSettings', ae.value); return; }
    if(ae && (ae.tagName==='INPUT' || ae.tagName==='TEXTAREA')) { send('showList',''); return; }
    // Otherwise, Esc hides the whole panel (matches NSPopover click-outside).
    send('hidePanel','');
  });
</script></body></html>`
}

func avatarHeader(title, subtitle string) string {
	return `<div class="header">
  <div class="avatar"><svg viewBox="0 0 24 24"><circle cx="8" cy="12" r="3.2" fill="#fff"/><circle cx="16" cy="12" r="3.2" fill="#fff"/><circle cx="8" cy="12" r="1.3" fill="#7c6cf0"/><circle cx="16" cy="12" r="1.3" fill="#7c6cf0"/></svg></div>
  <div class="htext"><h1>` + html.EscapeString(title) + `</h1><p>` + html.EscapeString(subtitle) + `</p></div>
</div>`
}

// RenderList is the account-list view: click a card to switch, Rescan / Quit
// in the footer.
// RenderList draws the account list.
//
// canAddAccount shows the card that starts the add-an-account flow. It is off
// where there is nothing behind it: today only the Windows Store build can create
// a profile, so offering the card elsewhere would be a button that does nothing.
// duplicateAccounts groups profiles by the account signed in to them and reports
// the ones sharing a single account: two profiles holding one account is a state
// to resolve, not a preference. It returns the set of folders to flag on the list,
// and the warning banner offering the merge for the first such group only — each
// merge needs Claude quit, so batching them would only lengthen the same
// interruption. Profiles with no account yet (empty UUID) are never grouped.
func duplicateAccounts(profiles []ProfileVM, esc func(string) string) (map[string]bool, string) {
	byUUID := map[string][]string{}
	nameByFolder := map[string]string{}
	var uuidOrder []string
	for _, p := range profiles {
		nameByFolder[p.Folder] = p.Name
		if p.UUID == "" {
			continue // no account signed in yet; nothing to be a duplicate of
		}
		if _, ok := byUUID[p.UUID]; !ok {
			uuidOrder = append(uuidOrder, p.UUID)
		}
		byUUID[p.UUID] = append(byUUID[p.UUID], p.Folder)
	}
	dupFolder := map[string]bool{}
	var firstGroup []string
	for _, u := range uuidOrder {
		g := byUUID[u]
		if len(g) < 2 {
			continue
		}
		for _, f := range g {
			dupFolder[f] = true
		}
		if firstGroup == nil {
			firstGroup = g
		}
	}
	if firstGroup == nil {
		return dupFolder, ""
	}
	a, b := firstGroup[0], firstGroup[1]
	nameOf := func(folder string) string {
		if n, ok := nameByFolder[folder]; ok {
			return n
		}
		return folder
	}
	// Two folders can carry the same display name; naming both "Claude" in the
	// warning would read as "Claude and Claude are the same account". When the names
	// collide, fall back to the folder, which is always distinct.
	labelA, labelB := nameOf(a), nameOf(b)
	if labelA == labelB {
		labelA, labelB = a, b
	}
	// The two folder names travel as data-* and are read back with dataset.
	// Interpolating them into the onclick string would reintroduce the v0.9.1 bug:
	// html.EscapeString turns an apostrophe into &#39;, the HTML parser decodes it
	// back to ' before the JS is parsed, and the handler breaks on any folder
	// containing one.
	warning := fmt.Sprintf(`<div class="dup">
  <div class="dt">%s 和 %s 是同一个账号。合并它们就能整理好。</div>
  <button class="btn-sm" data-dup-a="%s" data-dup-b="%s" onclick="mergePair(this.dataset.dupA,this.dataset.dupB)">合并</button>
</div>`, esc(labelA), esc(labelB), esc(a), esc(b))
	return dupFolder, warning
}

// wrenchSVG is the row-menu button's icon, drawn rather than set as a
// character. Every wrench codepoint in Unicode falls back to the colour emoji
// font on macOS, which would leave one full-colour icon in a panel of flat
// monochrome ones, and would render differently again in WebView2 on Windows.
// currentColor means the CSS above still owns the hover and open states.
const wrenchSVG = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" ` +
	`stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">` +
	`<path d="M7 10h3v-3l-3.5 -3.5a6 6 0 0 1 8 8l6 6a2 2 0 0 1 -3 3l-6 -6a6 6 0 0 1 -8 -8l3.5 3.5"/></svg>`

// rowMenu builds the wrench button and its menu for one
// account row. Both share it regardless of whether the row is the current
// account: the two actions do not depend on that, only askRemove's own choice
// of dialog (informational vs. destructive) does, decided client-side from
// data-current on the Remove item itself.
//
// removable is false only for the single row on a one-profile list (OnlyOne
// in the old Account settings screen): removing the last account would leave
// an empty panel with no way back, so Remove is not rendered at all, and the
// menu holds Rename alone — same rule as before, just relocated.
func rowMenu(esc func(string) string, p ProfileVM, removable bool) string {
	current := "0"
	if p.Current {
		current = "1"
	}
	removeItem := ""
	if removable {
		removeItem = fmt.Sprintf(`<button type="button" role="menuitem" class="danger" data-folder="%s" data-name="%s" data-convos="%d" data-current="%s" onclick="event.stopPropagation();askRemove(this)">从列表移除</button>`,
			esc(p.Folder), esc(p.Name), p.Convos, current)
	}
	return fmt.Sprintf(`<div class="rowmenu-wrap">
        <button type="button" class="chevbtn" aria-label="账号选项" aria-haspopup="menu" aria-expanded="false" onclick="event.stopPropagation();toggleRowMenu(this)">`+wrenchSVG+`</button>
        <div class="rowmenu" role="menu">
          <button type="button" role="menuitem" onclick="event.stopPropagation();startRename(this)">改名</button>
          %s
        </div>
      </div>`, removeItem)
}

// editRow is the inline rename control baked into every row alongside its
// normal .viewrow content; the card's own .renaming class (toggled by
// startRename/cancelRename in the shared script) decides which one shows, so
// there is no separate DOM to build or tear down. The Save button sends the
// same renameSave action and [folder, value] payload the old Account settings
// screen sent — the folder travels through card.dataset in the script, not
// interpolated here, so it needs no data-* attribute of its own.
func editRow(esc func(string) string, name string) string {
	return `<div class="editrow"><input class="rnrow" type="text" value="` + esc(name) + `" onclick="event.stopPropagation()" onkeydown="rowRenameKey(event,this)">` +
		`<button type="button" class="rncancel" onclick="event.stopPropagation();cancelRename(this)">取消</button>` +
		`<button type="button" class="rnsave" onclick="event.stopPropagation();rowRenameSave(this)">保存</button></div>`
}

func RenderList(profiles []ProfileVM, canAddAccount bool, status string) string {
	esc := html.EscapeString
	dupFolder, dupWarning := duplicateAccounts(profiles, esc)
	// status carries the result of an action that ended back on the list — a
	// merge that could not be computed, a recovery that came too late, a merge that
	// succeeded. Without it those paths re-render an unchanged-looking list and read
	// as having done nothing.
	statusBanner := ""
	if status != "" {
		statusBanner = `<div class="status">` + esc(status) + `</div>`
	}
	// Removing the only account listed would leave an empty panel with no way
	// back, so every row's menu hides Remove in that case (see rowMenu).
	removable := len(profiles) > 1
	var cards strings.Builder
	for _, p := range profiles {
		badge := planPill(p.Plan)
		dupPill := ""
		if dupFolder[p.Folder] {
			dupPill = `<span class="dup-pill">重复</span>`
		}
		menu := rowMenu(esc, p, removable)
		edit := editRow(esc, p.Name)
		if p.Current {
			cards.WriteString(fmt.Sprintf(`
      <div class="card current" data-folder="%s" data-name="%s"><div class="dotcur"></div>
        <div class="body"><div class="viewrow row1"><span class="name">%s</span>%s%s</div><div class="sub-current viewrow">正在使用</div>%s%s</div>%s</div>`,
				esc(p.Folder), esc(p.Name), esc(p.Name), badge, dupPill, usageBlock(p.Usage), edit, menu))
			continue
		}
		// A profile with no account yet is still switchable: switching to it is
		// how the user gets Claude open on it to sign in. Say so, otherwise the
		// card looks identical to a ready account and switching to it lands on
		// a login screen with no explanation.
		// A ready account has no subtitle: the whole card is the switch button,
		// and "Switch to this account" under every name only repeated that.
		sub := ""
		if !p.SignedIn {
			sub = `<div class="sub viewrow">还没登录。切换过来，然后登录。</div>`
		}
		// The card stays the switch target — clicking anywhere on it that is not
		// the chevron or the rename input still switches — except while it is
		// being renamed: the guard reads the very class startRename/cancelRename
		// toggle, so there is nothing to keep in sync separately.
		cards.WriteString(fmt.Sprintf(`
      <div class="card selectable" data-folder="%s" data-name="%s" onclick="if(!this.classList.contains('renaming'))askSwitch(this.dataset.folder,this.dataset.name)"><div class="chev">⇄</div>
        <div class="body"><div class="viewrow row1"><span class="name">%s</span>%s%s</div>%s%s%s</div>%s</div>`,
			esc(p.Folder), esc(p.Name), esc(p.Name), badge, dupPill, sub, usageBlock(p.Usage), edit, menu))
	}
	if len(profiles) == 0 {
		cards.WriteString(`<div class="empty">还没有管理任何账号。点「重新扫描」添加。</div>`)
	}
	if canAddAccount {
		// In the list rather than the footer: the footer already holds Rescan and
		// Settings, and a third labelled button does not fit in 400px without
		// shrinking the other two to bare icons.
		cards.WriteString(`
      <button class="addcard" onclick="send('newProfile','')">＋&nbsp; 添加另一个账号</button>`)
	}
	body := avatarHeader("Multi-Claude Switcher", "切换或管理你的 Claude 账号") +
		statusBanner + dupWarning + `<div class="cards">` + cards.String() + `</div>
<div class="footer">
  <button class="btn btn-light" onclick="send('showRescan','')">⟳&nbsp; 重新扫描</button>
  <button class="btn btn-light" onclick="send('showSettings','')">⚙&nbsp; 设置</button>
</div>
<div class="about">` + versionLink(core.Version) + `</div>`
	return shell(body)
}

// SettingsVM holds the state shown in the Settings view.
// MoreVM is the More screen's state. Only Busy: the backup that lives here
// reports through the progress card rather than a banner, and showMore clears
// any leftover banner on the way in, so a Status field would never be set.
type MoreVM struct {
	Busy bool
}

type SettingsVM struct {
	AutoSync   bool
	StartLogin bool
	Version    string
	Status     string // transient feedback banner (e.g. after a backup)
	Busy       bool   // a maintenance action is running; disable its button
}

func toggleClass(on bool) string {
	if on {
		return "toggle on"
	}
	return "toggle"
}

// RenderSettings is the in-panel Settings view: preferences and maintenance,
// reached from the gear on the account list. Back arrow returns to the list.
func RenderSettings(vm SettingsVM) string {
	status := ""
	if vm.Status != "" {
		status = `<div class="status">` + html.EscapeString(vm.Status) + `</div>`
	}
	// The update check is disabled while a maintenance action runs, the same as
	// the button it used to be. As a link that means greying it and dropping the
	// handler, since a link has no disabled attribute to honour.
	update := `<button class="aboutlink" onclick="send('checkUpdates','')">检查更新</button>`
	if vm.Busy {
		update = `<button class="aboutlink" disabled>检查更新</button>`
	}
	body := `<div class="header">
  <button class="back" onclick="send('showList','')">‹</button>
  <div class="htext"><h1>设置</h1><p>偏好与维护</p></div>
</div>` + status + `
<div class="slist">
  <div class="srow"><div class="slabel"><div class="t">切换时自动同步</div><div class="s">切换账号时合并两边的 Code 对话</div></div>
    <div class="` + toggleClass(vm.AutoSync) + `" onclick="send('toggleAutoSync','')"></div></div>
  <div class="srow"><div class="slabel"><div class="t">开机自启动</div><div class="s">登录系统时启动 Multi-Claude Switcher</div></div>
    <div class="` + toggleClass(vm.StartLogin) + `" onclick="send('toggleLogin','')"></div></div>
  <button class="sbtn" onclick="send('showMore','')">更多<span class="navchev">›</span></button>
  <button class="sbtn danger" onclick="send('quit','')">退出 Multi-Claude Switcher</button>
</div>
<div class="about">
  ` + versionLink(vm.Version) + `
  <span class="sep">·</span>
  ` + update + `
  <span class="sep">·</span>
  <button class="aboutlink" onclick="send('showDebug','')">报告问题</button>
</div>`
	return shell(body)
}

// RenderMore is everything that acts on conversation data, gathered off the
// Settings screen so what remains there is what a person actually adjusts.
//
// Named More rather than Conversations, which was tried first: one of these
// four opens the archive folder, which holds removed accounts rather than
// conversations, so the more descriptive name was the less accurate one.
func RenderMore(vm MoreVM) string {
	// The backup is the one thing here that takes time, so it is the one thing
	// disabled while a maintenance action runs. The folder shortcuts and the
	// sync screen are safe to reach at any moment.
	backup := `<button class="sbtn" onclick="send('backup','')">备份所有账号</button>`
	if vm.Busy {
		backup = `<button class="sbtn" disabled>备份所有账号</button>`
	}
	body := `<div class="header">
  <button class="back" onclick="send('showSettings','')">‹</button>
  <div class="htext"><h1>更多</h1><p>同步、备份和文件存放位置</p></div>
</div>
<div class="slist">
  <button class="sbtn" onclick="send('showSync','')">在账号之间同步<span class="navchev">›</span></button>
  ` + backup + `
  <button class="sbtn" onclick="send('openBackups','')">打开备份文件夹</button>
  <button class="sbtn" onclick="send('openArchive','')">打开归档文件夹</button>
</div>`
	return shell(body)
}

type DebugVM struct {
	Report  string
	Comment string
	Status  string // transient feedback, e.g. after Copy
}

func RenderDebug(vm DebugVM) string {
	esc := html.EscapeString
	status := ""
	if vm.Status != "" {
		status = `<div class="status">` + esc(vm.Status) + `</div>`
	}
	reportBox := `<div class="dbgbox">` + esc(vm.Report) + `</div>`
	body := `<div class="header">
  <button class="back" onclick="send('showSettings', document.getElementById('dbgc').value)">‹</button>
  <div class="htext"><h1>报告问题</h1><p>报告里到底包含什么</p></div>
</div>` + status + `
<div class="dbgnote">你的邮箱、账号 ID、用户名和主目录在下面都换成了 account-1 这样的代称。你自己给配置文件夹起的名字仍可能出现。` + esc(diagnostics.RedactedMarker) + ` 标出的是看起来像地址或 ID、已被遮住的内容。</div>
` + reportBox + `
<div class="hint">出了什么问题？（选填）</div>
<textarea class="dbgarea" id="dbgc" placeholder="切换到工作账号后，个人账号被关掉了…">` + esc(vm.Comment) + `</textarea>
<div class="footer">
  <button class="btn btn-light" style="flex:none;padding:10px 14px" onclick="send('copyDebug', document.getElementById('dbgc').value)">复制</button>
  <button class="btn btn-primary" onclick="askReport()">报告问题</button>
</div>`
	return shell(body)
}

// RenderRescan is the in-panel Rescan view: check the accounts to manage.
// Ghost accounts are shown read-only. Cancel / Confirm in the footer. No
// separate window — this replaces the panel content and swaps back on confirm.
func RenderRescan(accounts []core.ScannedAccount, preselected map[string]bool) string {
	esc := html.EscapeString
	var cards strings.Builder
	for _, a := range accounts {
		date := "暂无日期"
		if !a.LastUpdated.IsZero() {
			date = a.LastUpdated.Format("2006-01-02")
		}
		if a.SignedOut || a.Pending {
			// A profile folder with no account in it yet. Selectable on
			// purpose: managing it is what puts it in the account list, which
			// is how the user gets to switch to it and sign in.
			//
			// Pending rows — profiles MCS has just made and told the user to go
			// and sign in to — belong here rather than with the ghosts below.
			// They have no account UUID, so the ghost branch claimed them and
			// drew them as "Unrecognized account": no name, no tick box, and a
			// warning about a folder the user had asked for thirty seconds
			// earlier.
			sel, chk := "", ""
			if preselected[a.HomeFolder] {
				sel, chk = " selected", " checked"
			}
			cards.WriteString(fmt.Sprintf(`
      <div class="card selectable%s" data-folder="%s" onclick="toggleCard(this)">
        <input type="checkbox" class="chk"%s>
        <div class="body"><div class="row1"><span class="name">%s</span></div>
          <div class="note-todo">%s</div></div></div>`,
				sel, esc(a.HomeFolder), chk, esc(a.HomeFolder), esc(a.Note)))
			continue
		}
		if !a.Complete {
			if a.Recoverable {
				// Not selectable: there is no folder to manage yet. The action is
				// to give this account one, which is what Recover does. The note
				// is deliberately blue — nothing here is broken, the conversations
				// are intact and only the profile is missing. The action carries
				// only the UUID: the source paths are re-read from a fresh scan
				// when recovery runs, since they are valid only for the scan that
				// produced them.
				cards.WriteString(fmt.Sprintf(`
      <div class="card"><div style="width:21px;flex:none"></div>
        <div class="body"><div class="row1"><span class="name">已在 Claude Desktop 里退出登录</span></div>
          <div class="meta"><span class="chip">%s</span><span class="dot">·</span>%d 个对话<span class="dot">·</span>%s</div>
          <div class="note-todo">%s</div></div>
        <button class="btn-sm" data-uuid="%s" onclick="send('showRecover',this.dataset.uuid)">恢复</button></div>`,
					esc(ShortID(a.UUID)), a.Convos, esc(date), esc(a.Note),
					esc(a.UUID)))
				continue
			}
			cards.WriteString(fmt.Sprintf(`
      <div class="card ghost"><div style="width:21px;flex:none"></div>
        <div class="body"><div class="row1"><span class="name">无法识别的账号</span></div>
          <div class="meta"><span class="chip">%s</span><span class="dot">·</span>%d 个对话<span class="dot">·</span>%s</div>
          <div class="note-bad">%s</div></div></div>`,
				esc(ShortID(a.UUID)), a.Convos, esc(date), esc(a.Note)))
			continue
		}
		name := a.Email
		if name == "" {
			name = a.HomeFolder
		}
		badge := ""
		switch a.Account {
		case core.AccountTeam:
			badge = `<span class="pill pill-team">🏢 Team</span>`
		case core.AccountPersonal:
			badge = `<span class="pill pill-personal">个人</span>`
		}
		sel, chk := "", ""
		if preselected[a.HomeFolder] {
			sel, chk = " selected", " checked"
		}
		cards.WriteString(fmt.Sprintf(`
      <div class="card selectable%s" data-folder="%s" onclick="toggleCard(this)">
        <input type="checkbox" class="chk"%s>
        <div class="body"><div class="row1"><span class="name">%s</span>%s</div>
          <div class="meta"><span class="chip">%s</span><span class="dot">·</span>%d 个对话<span class="dot">·</span>%s</div></div></div>`,
			sel, esc(a.HomeFolder), chk, esc(name), badge, esc(ShortID(a.UUID)), a.Convos, esc(date)))
	}
	body := `<div class="header">
  <button class="back" onclick="send('showList','')">‹</button>
  <div class="htext"><h1>选择账号</h1><p>要管理哪些账号？</p></div>
</div>
<div class="cards">` + cards.String() + `</div>
<div class="footer">
  <button class="btn btn-light" onclick="send('showList','')">取消</button>
  <button class="btn btn-primary" onclick="confirmManaged()">确认</button>
</div>`
	return shell(body)
}

// NewProfileVM drives the name-the-profile screen. One screen serves both
// entry points: RecoverUUID empty is the plain add path, set is a recovery of
// that account. They run the same underlying operation and differ only in copy
// and in whether a session bucket comes along, so sharing the view is what keeps
// the two from drifting apart.
type NewProfileVM struct {
	RecoverUUID   string
	SuggestedName string
	Convos        int
	Err           string
}

// RenderNewProfile is the in-panel screen for naming a new account profile.
func RenderNewProfile(vm NewProfileVM) string {
	esc := html.EscapeString
	recovering := vm.RecoverUUID != ""

	title, sub, confirm := "添加另一个账号", "它会有自己独立的配置文件夹", "添加"
	if recovering {
		title, sub, confirm = "恢复这个账号", "它会有自己独立的配置文件夹", "恢复"
	}

	// "different account" stays one unbroken phrase inside the <b>, so a test can
	// assert on it as the user reads it. ShortID is the account's first 8 characters,
	// so the copy says "starting", not "ending".
	second := `<div class="hintw">请登录一个<b>不同的账号</b>。登录已有的账号会产生重复，MCS 会请你合并。</div>`
	if recovering {
		second = fmt.Sprintf(`<div class="hintw">请登录开头为 <b>%s</b> 的账号（%d 个对话）。它的对话会自动回来。</div>`,
			esc(ShortID(vm.RecoverUUID)), vm.Convos)
	}

	errBlock := ""
	if vm.Err != "" {
		errBlock = `<div class="errbox">` + esc(vm.Err) + `</div>`
	}

	body := `<div class="header">
  <button class="back" onclick="send('showList','')">‹</button>
  <div class="htext"><h1>` + esc(title) + `</h1><p>` + esc(sub) + `</p></div>
</div>` + errBlock + `
<input id="np" class="rninput" type="text" value="` + esc(vm.SuggestedName) + `" placeholder="个人">
<div class="hint">Claude 会关闭，当前账号会保存好，然后打开一个全新的 Claude。</div>` + second + `
<div class="footer">
  <button class="btn btn-light" onclick="send('showList','')">取消</button>
  <button class="btn btn-primary" data-uuid="` + esc(vm.RecoverUUID) + `" onclick="createProfileSave(this)">` + esc(confirm) + `</button>
</div>
<script>var e=document.getElementById('np'); e.focus(); e.select();</script>`
	return shell(body)
}

// MergeCandidateVM is one side of a duplicate pair on the merge screen.
type MergeCandidateVM struct {
	Folder  string
	Name    string
	Plan    string
	Convos  int
	Current bool // the profile Claude is running on right now
}

// RenderMerge is the in-panel screen for resolving two profiles signed in to one
// account. The profile in use is preselected to keep, because keeping it means
// the user does not have to sign in again; they can pick the other one when they
// prefer its name. Conversations are combined either way, so the choice only
// decides which name survives.
//
// plan is what the merge will actually do, computed by core.MergePreview before
// this screen is rendered. The total shown comes from there and not from adding the
// two counts: a conversation both profiles hold is one conversation afterwards, not
// two.
func RenderMerge(a, b MergeCandidateVM, plan core.MergePlan, status string, busy bool) string {
	esc := html.EscapeString
	st := ""
	if status != "" {
		st = `<div class="status">` + esc(status) + `</div>`
	}
	// Keep the in-use profile by default. If neither is running, fall back to the
	// first, so the screen is never rendered with nothing chosen.
	keep := a.Folder
	if b.Current && !a.Current {
		keep = b.Folder
	}

	card := func(c MergeCandidateVM) string {
		cls := "card selectable"
		if c.Folder == keep {
			cls += " selected"
		}
		sub := "将被归档"
		if c.Folder == keep {
			sub = "保留这个"
			if c.Current {
				sub = "正在使用 · 保留这个"
			}
		}
		return fmt.Sprintf(`
      <div class="%s" data-folder="%s" onclick="toggleMergePick(this)">
        <input type="checkbox" class="chk"%s>
        <div class="body"><div class="row1"><span class="name">%s</span>%s</div>
          <div class="meta">%d 个对话<span class="dot">·</span>%s</div></div></div>`,
			cls, esc(c.Folder), map[bool]string{true: " checked", false: ""}[c.Folder == keep],
			esc(c.Name), planPill(c.Plan), c.Convos, esc(sub))
	}

	dis, oc := "", `onclick="mergeConfirm()"`
	if busy {
		dis, oc = " disabled", ""
	}

	// Where both profiles hold a record and the keeper's copy is the newer one, the
	// sync leaves the keeper's alone and the archive keeps the other. Say so before
	// the user commits: after the merge that version is reachable only by opening the
	// archive folder.
	conflictNote := ""
	if plan.Conflicts > 0 {
		conflictNote = fmt.Sprintf(`<div class="hintw">有 %d 个对话两边都有，而且上次同步后都改过。会保留较新的版本，另一份留在归档文件夹里，可以从设置里打开。</div>`, plan.Conflicts)
	}
	// Say so rather than quietly delivering a smaller number than promised.
	if plan.Unreadable > 0 {
		conflictNote += fmt.Sprintf(`<div class="hintw">有 %d 个文件读不了，会留在原处不动。</div>`, plan.Unreadable)
	}

	body := `<div class="header">
  <button class="back" onclick="send('showList','')">‹</button>
  <div class="htext"><h1>合并重复账号</h1><p>两个都是同一个账号</p></div>
</div>` + st + `
<div class="cards">` + card(a) + card(b) + `</div>
<div class="hint">全部 ` + fmt.Sprint(plan.Combined) + ` 个对话会合并到你保留的账号里。另一个文件夹会归档而不是删除，你可以自己放回去。</div>` + conflictNote + `
<div class="footer">
  <button class="btn btn-light" onclick="send('showList','')">取消</button>
  <button class="btn btn-primary"` + dis + ` ` + oc + `>合并</button>
</div>`
	return shell(body)
}

// RenderSync is the in-panel Sync view: one card per direction (From → To).
// Tapping a direction copies that account's Code sessions into the other.
func RenderSync(profiles []ProfileVM, status string, busy bool) string {
	esc := html.EscapeString
	st := ""
	if status != "" {
		st = `<div class="status">` + esc(status) + `</div>`
	}
	var cards strings.Builder
	count := 0
	waiting := 0
	for _, p := range profiles {
		if !p.SignedIn {
			waiting++
		}
	}
	for _, from := range profiles {
		for _, to := range profiles {
			if from.Folder == to.Folder {
				continue
			}
			// Sessions are stored per account, so a profile with no account
			// signed in has no bucket to read from or write to. Offering the
			// direction anyway just fails at the point of clicking it.
			if !from.SignedIn || !to.SignedIn {
				continue
			}
			count++
			// No per-destination caveat. A Team account used to be marked as one
			// that could not receive an import; that was MCS filing conversations
			// under the wrong organization, not a limit of the account.
			oc, cls := "", "card selectable"
			if busy {
				cls = "card ghost"
			} else {
				// Through askSync, never straight to send('sync'): this closes the
				// user's Claude, so it needs their yes first. The names and count
				// ride along so the dialog can say which way the copy goes and how
				// much moves — "Sync?" alone does not tell the user that.
				oc = fmt.Sprintf(`data-from="%s" data-to="%s" data-from-name="%s" data-to-name="%s" data-convos="%d" `+
					`onclick="askSync(this.dataset.from,this.dataset.to,this.dataset.fromName,this.dataset.toName,this.dataset.convos)"`,
					esc(from.Folder), esc(to.Folder), esc(from.Name), esc(to.Name), from.Convos)
			}
			cards.WriteString(fmt.Sprintf(`
      <div class="%s" %s><div class="chev">→</div>
        <div class="body"><div class="row1"><span class="name">%s → %s</span></div>
          <div class="sub">把 %s 的对话复制到 %s</div></div></div>`,
				cls, oc, esc(from.Name), esc(to.Name), esc(from.Name), esc(to.Name)))
		}
	}
	if count == 0 {
		cards.WriteString(`<div class="empty">至少要管理两个账号，才能在它们之间同步。</div>`)
	}
	if waiting > 0 {
		cards.WriteString(fmt.Sprintf(
			`<div class="empty">有 %d 个账号还没登录，所以不能同步。请先切换过去登录。</div>`,
			waiting))
	}
	body := `<div class="header">
  <button class="back" onclick="send('showMore','')">‹</button>
  <div class="htext"><h1>在账号之间同步</h1><p>Claude 会关闭，然后回到你原来的账号重新打开</p></div>
</div>` + st + `
<div class="cards">` + cards.String() + `</div>`
	return shell(body)
}

// ComputePreselect returns the folders to pre-check in the Rescan view.
func ComputePreselect(accounts []core.ScannedAccount, managed []string) map[string]bool {
	firstRun := managed == nil
	set := map[string]bool{}
	for _, m := range managed {
		set[m] = true
	}
	pre := map[string]bool{}
	for _, a := range accounts {
		// Folders awaiting sign-in count too: they are switch targets, and
		// leaving them unchecked on a first run is what hides the profile the
		// user set up precisely so they could sign in to it.
		if (a.Complete || a.SignedOut) && (firstRun || set[a.HomeFolder]) {
			pre[a.HomeFolder] = true
		}
	}
	return pre
}

// RemovedVM drives the screen shown when a removal did not go cleanly: it
// failed outright, or it moved the folder but left something behind. A clean
// removal no longer reaches this screen at all — it goes straight back to the
// list with a status banner instead, since the row disappearing from the list
// is confirmation enough. See RenderList and both hosts' removeProfile case.
type RemovedVM struct {
	Folder string // for Try again after a failure
	Name   string
	Convos int
	// Err is set when the folder did NOT move at all: the whole removal failed.
	Err string
	// RegistryNote is set when the folder DID move but something it left
	// behind (its display name, its managed listing, ...) could not be
	// cleared. The hosts never set both Err and RegistryNote, and never
	// construct a RemovedVM with neither set: a removal that left nothing
	// behind returns to the list instead of reaching this screen. That rule is
	// enforced where it can be checked without a live user in the room:
	// DecideRemovalOutcome is a pure function with its own tests, and it is the
	// only production code that builds a RemovedVM. RenderRemoved itself does
	// not re-check it (see RenderRemoved's doc comment for why).
	RegistryNote string
	// Status is the host's transient line. Try again re-runs a removal that can
	// take seconds, and without somewhere to show progress the screen redraws
	// identically and the button reads as dead.
	Status string
}

// RenderRemoved is the screen shown when a removal could not be completed
// (Err set), or completed but left something behind (RegistryNote set) — the
// two cases that still have something to say once a clean removal returns
// straight to the list with a banner instead of a screen of its own. A
// removal that only reported itself in a line at the top of a changed list is
// the case where the user cannot tell whether it happened, which for a
// destructive-looking action is the one thing this screen has to answer.
//
// If neither Err nor RegistryNote is set, this deliberately falls through to
// the same rendering as a registry complaint with an empty note: a plain
// "<name> removed" screen with a Done button. That is a harmless, truthful
// state to land in by mistake — the removal genuinely is not undone by
// seeing it — so it is the fallback for a host that regressed the routing in
// DecideRemovalOutcome, not a panic. This function is called straight from
// each host's reloadPanel, on goroutines and webview callbacks neither host
// recovers around; a panic here does not surface a bug to a developer, it
// takes down the user's whole panel mid-removal, on the machine where they
// were just told their account was being removed. A second, weaker copy of
// an invariant that DecideRemovalOutcome's own tests already pin is not worth
// that trade.
func RenderRemoved(vm RemovedVM) string {
	esc := html.EscapeString

	// The host's transient line. Try again re-runs a removal that can take
	// seconds, and without this the screen redraws identically and the button
	// reads as dead.
	st := ""
	if vm.Status != "" {
		st = `<div class="status">` + esc(vm.Status) + `</div>`
	}

	if vm.Err != "" {
		// Folder travels as data-* and is read back with dataset, never
		// interpolated into the onclick string: html.EscapeString turns an
		// apostrophe into &#39;, the HTML parser decodes it back to ' before the
		// inline JS is parsed, and a display name or folder containing one would
		// break the handler (the v0.9.1 bug).
		body := `<div class="header">
  <button class="back" onclick="send('showList','')">‹</button>
  <div class="htext"><h1>` + esc(vm.Name) + ` 没有移除</h1><p>什么都没有动</p></div>
</div>
` + st + `<div class="errbox">` + esc(vm.Err) + `</div>
<div class="hint">这个账号还在列表里，你可以再试一次。</div>
<div class="footer">
  <button class="btn btn-light" onclick="send('showList','')">返回</button>
  <button class="btn btn-primary" data-folder="` + esc(vm.Folder) + `" onclick="send('removeProfile',this.dataset.folder)">再试一次</button>
</div>`
		return shell(body)
	}

	// One sentence, saying the only two things the user needs: where it went, and
	// that it is not gone. The archived folder's own name and a button to open the
	// archive both used to be here; they made the screen busy for a fact almost
	// nobody acts on, and Settings already has a way into that folder. This line
	// only reaches the screen alongside a registry complaint now — a clean removal
	// with nothing left behind returns straight to the list instead — but it still
	// answers the question this screen exists for: the folder did move.
	//
	// Same zero/one/many shape as askRemove's own wording in the shell script
	// (worded differently there, since that one asks before the fact and this one
	// reports after it): zero gets its own phrase rather than falling through to a
	// plural that would read "0 conversations", and a freshly created,
	// never-signed-in profile is not a rare case to remove.
	what := "它的文件夹在归档里，没有删除。"
	if vm.Convos > 0 {
		what = fmt.Sprintf("它的 %d 个对话在归档里，没有删除。", vm.Convos)
	}
	// A registry that could not be cleared is not styled as an error: the folder
	// really did move, which is the thing this screen exists to confirm. But it
	// cannot be silent either — a display name left behind is inherited, without
	// warning, by any later account that reuses this identity, and this screen is
	// the only place that will ever say so.
	// One line per complaint. The note carries errors.Join output, whose entries
	// are separated by newlines, and HTML collapses those: two separate things
	// that could not be cleared ran together into one sentence that read as
	// neither of them.
	registryNote := ""
	if vm.RegistryNote != "" {
		var lines strings.Builder
		for _, line := range strings.Split(vm.RegistryNote, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				lines.WriteString(`<div class="noteline">` + esc(line) + `</div>`)
			}
		}
		registryNote = `<div class="hintw">` + lines.String() + `</div>`
	}
	body := `<div class="header">
  <div class="htext"><h1>` + esc(vm.Name) + ` 已移除</h1><p>它已经不在切换器里了</p></div>
</div>
` + st + `<div class="hint">` + esc(what) + `</div>
` + registryNote + `
<div class="footer">
  <button class="btn btn-primary" onclick="send('showList','')">完成</button>
</div>`
	return shell(body)
}

// ShortID trims a UUID to its leading 8 characters for compact display.
func ShortID(uuid string) string {
	if len(uuid) > 8 {
		return uuid[:8]
	}
	return uuid
}
