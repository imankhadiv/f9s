# f9s Design Standards

Evidence-based rules for all f9s UI work, distilled from how the best terminal
UIs (k9s, lazygit, helix, zellij, btop, the Charm and Textualize ecosystems)
are designed. PRs that touch the UI are reviewed against this document.

## Interaction grammar

1. **Single-key mnemonic verbs + `:` command mode.** Navigation by
   `:<resource>` (`:jobs`, `:tm`, `:ckpt`, `:sp`), actions by one mnemonic
   key per view. Aliases mirror Flink nouns.
2. **Three discoverability layers**: (a) the always-visible footer hint bar,
   (b) a `?` overlay with the full keymap grouped view-specific / global /
   navigation, (c) docs. Lazygit's `?` cheatsheet is the canonical reason a
   dense tool stays approachable.
3. **Same key, same meaning — enforced.** One global verb table
   (`Enter`=drill-in, `Esc`=back/out, `/`=filter, `l`=logs, `ctrl-…`
   reserved for destructive). Views may add keys but never repurpose global
   ones. Lint this in review.
4. **Shallow, escapable modality.** Modes (command, filter, confirm) are
   transient, always `Esc`-outable, and named in the footer while active.
5. **Friction proportional to blast radius.** Destructive ops confirm in a
   modal that names the exact resource and its state ("Cancel job
   `fraud-detector` (running 3d)?"). Stop-with-savepoint vs hard-cancel is
   an explicit choice, never a default.

## Information design

6. **Show the state transition.** Flash the outcome of every action; add a
   toggleable REST-call log (the exact Flink API calls f9s issued) — the
   lazygit command-log lesson: trust plus education.
7. **Dense but scannable tables.** Fixed terse headers, right-aligned
   numerics, color carries *status* only: green=RUNNING/healthy,
   amber=RESTARTING/backpressured/transitional-warning, red=FAILED,
   dim=terminal/secondary. Never invent per-view colors.
8. **Progressive disclosure**: list → detail → deep-dive
   (jobs → vertices → subtasks → logs/exceptions/checkpoints), with
   `Enter`/`Esc` symmetry guaranteed at every level (the view stack +
   breadcrumbs model this).
9. **Designed empty/loading/error states.** Stale-over-frozen is policy, but
   staleness must be visible: data age in the header ("⟳ 2s ago"), spinner
   only on first load, error banner *above* the last-good table (never
   replacing it), and purposeful empty states that say what to do next.
10. **The header answers: which cluster, which view, how many rows, how
    fresh.** In multi-cluster ops, acting on the wrong cluster is the real
    hazard — identity stays visible always (per-cluster skins exist in k9s
    precisely for this).

## Rendering & performance

11. **Keep Update/View fast; render only the viewport.** bubbletea repaints
    the full frame and line-diffs it; the UI is only as responsive as
    Update()/View(). Virtualize tables (style only visible rows), precompute
    styled cells when data arrives — never inside View() — and never do IO in
    Update: return tea.Cmds.
12. **Poll with tea.Tick; guard staleness.** Commands complete out of order:
    tag fetches with a generation counter and drop responses older than the
    last applied; pause polling for views not on top; per-view intervals.
    **A generation counter is not enough on its own** — views are rebuilt,
    not reused (`:`-commands reset the stack, and a context switch rebuilds
    every view against a different cluster's client), and the replacement
    starts its counter over. Any message carrying an async result back to a
    view must also name the view that asked (`owner *T`), and Update must
    drop messages whose owner is not itself. Messages only ever reach
    `a.top()`, so a late reply otherwise lands on whatever now occupies that
    slot: this has bitten twice, once showing another cluster's jobs and once
    submitting a jar to the wrong cluster entirely. When moving a decision
    out of a `tea.Cmd` closure into Update, check what the closure had
    captured — captured state travels with the message, state read from the
    receiver does not.
13. **Assume hostile terminals.** Don't bypass the renderer (it already
    overwrites instead of clearing). Test in tmux, Windows Terminal, and over
    SSH; provide ASCII fallbacks for non-box-drawing glyphs; regression-test
    with teatest and VHS tapes.

## Theming & customization

14. **Semantic style tokens + YAML skins, ANSI-16-friendly.** A token layer
    (`status.running`, `table.header`, `modal.border`) sits between lipgloss
    and views; defaults adapt light/dark; overrides load from `skin.yaml`
    (k9s skins are the de-facto format). Honor `NO_COLOR` and terminals that
    want their own 16 colors.
15. **Keymap config only with a single source of truth.** Footer hints and
    the `?` overlay are generated from `key.Binding` data — never
    hand-written strings — so rebinding can never desynchronize the docs
    from the keys (zellij's custom-keymap failure mode).

## Current gaps (queued in BACKLOG)

1. Keymaps are not user-configurable. Rule 15 wants one source of truth
   feeding both the footer hints and the bindings before that ships.
2. The footer advertises at most `maxFooterKeys` (9) of a view's own keys, so
   dense views lean on `?` to be discoverable at all — rule 2's layer (a) is
   lossy exactly where it is needed most.
3. No ASCII fallback and no `NO_COLOR` handling: box-drawing, the `⚠`/`✓`/`⟳`
   glyphs and the gradient wordmark are all assumed renderable (rule 13).
4. No teatest coverage. The VHS tapes in `vhs/` are demos, not assertions, so
   rule 13's "regression-test with teatest and VHS tapes" is half-built.

Shipped since this list was written: `:` command mode with ambiguity
reporting, the semantic token/skin layer (`internal/ui/styles`, `LoadSkin`),
fetch generation guards and view-ownership tags (rule 12), and a data-age
indicator in the header.

## Sources

k9s skins & docs (k9scli.io), Jesse Duffield "Lazygit 5 Years On",
bwplotka.dev/2025/lazygit, leg100.github.io "Building bubbletea programs",
bubbletea standard_renderer.go + issues #577/#1019, lipgloss #439,
Textualize "7 things I've learned building a modern TUI framework",
zellij keybindings docs + issue #4119, k9s issues #1234/#294, btop #139,
helix keymap docs + discussion #1511.
