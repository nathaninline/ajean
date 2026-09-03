# Translating ajean's UI

The web UI has no external translation service and no build-time dependency
beyond Go — every string lives in one file, `internal/ajean/ui/src/js/00a-i18n-data.js`,
as a plain JavaScript object: one block per language, same keys in every
block, French (`fr`) is the source of truth.

```js
var I18N = {
fr: {
  "appearance.dark_mode": "mode sombre",
  ...
},
en: {
  "appearance.dark_mode": "dark mode",
  ...
},
...
};
```

You do **not** need to touch any other file, and you do **not** need to know
Go, JavaScript, or how ajean's UI is built, to translate. You do need a text
editor and, ideally, someone with the Go toolchain to build and check in the
result for you if you can't run the commands below yourself (see "Building
and previewing" at the end).

## Adding a brand-new language

1. Open `00a-i18n-data.js`. Copy the entire `fr: { ... }` block (from `fr: {`
   down to its matching `},`).
2. Paste the copy right after it (order inside the file doesn't matter), and
   change `fr:` to your language's two-letter code (`pt` for Portuguese, `ja`
   for Japanese, etc. — use standard ISO 639-1 codes).
3. Translate every **value** (the text after the `:`). **Never change a key**
   (the text before the `:`, in quotes) — the app looks strings up by key, a
   changed key just means that string silently falls back to French instead
   of erroring, which is easy to miss.
4. Open `00b-i18n.js` and add your language to `LANG_NAMES` near the top:
   ```js
   var LANG_NAMES = {
     fr: 'Français', en: 'English', ...,
     pt: 'Português'   // <- add this
   };
   ```
   That's the only other file that needs a change — the language dropdown in
   Settings reads this object and builds its own list automatically.

## Completing an existing (partial) language

Same as above, minus step 1-2: just fill in missing values in that language's
existing block. Use the verification tool (below) to find out exactly which
keys are still missing.

## The tricky part: sentences split around a variable

Some keys come in pairs — a `_prefix` and a `_suffix` (or `_before`/`_after`,
`_mid`) — because the original French sentence has a variable (a filename, a
number, someone's input) stitched into the middle of it. For example:

```js
"chat.session.delete_confirm_prefix": "Supprimer définitivement « ",
"chat.session.delete_confirm_suffix": " » ? Cette action est irréversible.",
```

At runtime the code does `prefix + theSessionName + suffix`, producing e.g.
*"Supprimer définitivement « Mon projet » ? Cette action est irréversible."*

When translating a pair like this, don't translate each half as if it were a
complete, standalone sentence — translate the **whole conceptual sentence**
first, then split it at the same place the variable goes. Keep any spacing or
punctuation right up against where the variable will be inserted (a French
`« ` opening quote right before the split point becomes an English `"` in the
same position, not on its own line, not with a stray space added or removed).
If your language's word order would naturally put the variable somewhere else
in the sentence than French does, that's fine — just make sure `prefix +
variable + suffix` still reads as one grammatical sentence when concatenated.

HTML tags work the same way: a value like `Les bulles arrivent <b>déjà
fermées</b> au lieu de...` needs its `<b>...</b>` kept in the translation,
wrapping the equivalent translated phrase, in whatever position your
language's grammar puts it.

## Checking your work

From the repository root, with Go installed:

```bash
go run ./tools/verify-i18n
```

This reports, per language, how many of the reference keys (against `fr`)
are present, and lists exactly which ones are still missing (or, if a key
was accidentally retyped instead of copy-pasted, which ones don't match any
French key — almost always a typo). A language doesn't need to be 100%
translated to be useful — `t()` (the lookup function, in `00b-i18n.js`) falls
back to French for any key missing in the active language, so a partial
translation degrades gracefully instead of breaking anything.

It also prints a `⚠` warning for any key whose value you left **identical**
to the French one — a strong sign it got copy-pasted and never actually
translated (the key is present, so the missing-keys check above won't catch
it). This happened for real early in this project: several section titles
(`device.title`, `tasks.title`, `settings.title`…) were accidentally copied
straight from English text into the French block itself and shipped that
way for a while, unnoticed, because nothing checked value content — only
presence. Some identical values are fine on purpose (this app borrows
plenty of English tech words verbatim — `preset`, `backend`, `session`,
`API`…), and those are pre-approved in a short allowlist inside
`tools/verify-i18n/main.go` so they don't nag you every run. If your
language's translation deliberately keeps a term as a loanword too, that's
a legitimate choice — the warning is informational, not a failure, so it
won't block your contribution. If you get a `⚠` for a string you just
hadn't gotten to yet, that's the tool doing its job — go translate it.

## Building and previewing your translation

If you have Go installed:

```bash
go generate ./internal/ajean   # rebuilds internal/ajean/ui/index.html from src/
go build ./cmd/ajean           # rebuilds the ajean binary with your translation baked in
```

Run the resulting binary, open the web UI, and pick your language from the
dropdown in the Appearance panel to see it live — no reload needed, it
re-renders the whole page immediately.

If you don't have Go set up, that's fine: translate the JSON-shaped values in
`00a-i18n-data.js`, run `go run ./tools/verify-i18n` if you can (or just be
extra careful to keep every key exactly as-is), and hand the file to whoever
is merging your contribution — they can build and spot-check it from there.
