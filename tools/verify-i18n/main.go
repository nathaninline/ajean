// verify-i18n checks internal/ajean/ui/src/js/00a-i18n-data.js for completeness:
// every language block must have exactly the same set of keys as fr (the
// source language). Run it after editing a translation, before opening a PR.
//
//	go run ./tools/verify-i18n
//
// It also flags a second, sneakier problem: a value that was never actually
// translated — left identical to the fr reference — instead of just missing.
// This slips past the key-completeness check because the key IS present.
// It happened for real during this project's original French extraction:
// several section titles (Device, Tasks, Link, Engine, Settings…) got copied
// straight from the English-labeled source HTML into the "fr" slot verbatim.
// Some identical values are legitimate on purpose — this app borrows plenty
// of English tech words into its French text (preset, backend, session,
// vision…) — those are listed in knownLoanwords below. Anything else that
// matches fr exactly is printed as a warning worth a human glance; it is
// NOT treated as a failure, since a translator may legitimately decide a
// new term is a fine loanword in their language too.
//
// Exit code 0 = all languages complete (loanword warnings don't affect this).
// Non-zero = a key is missing or extra somewhere; details printed to stdout.
package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"
)

const dataFile = "internal/ajean/ui/src/js/00a-i18n-data.js"

// Matches "lang: {" at the start of a language block, and a top-level "key": "value" line.
var reLangStart = regexp.MustCompile(`(?m)^(\w+):\s*\{\s*$`)
var reEntry = regexp.MustCompile(`(?m)^\s*"((?:[^"\\]|\\.)*)":\s*"((?:[^"\\]|\\.)*)",?\s*$`)

// knownLoanwords lists keys where every language is allowed to legitimately
// keep the same value as fr — technical jargon, acronyms, product/algorithm
// names, or short words this app already borrows from English into French
// on purpose (preset, backend, session…). Vetted by hand against the fr
// dictionary's own established vocabulary when this list was built — see
// the commit that introduced it. Add to this list when a translator makes a
// deliberate, reviewed call to keep a term as a loanword in their language;
// don't add to it just to silence a warning without checking.
var knownLoanwords = map[string]bool{
	"agent.title": true, "api.title": true, "attach.too_big_max": true,
	"settings.title": true, "projects.new_project_button": true,
	"bench.title": true, "chat.decode": true, "chat.decode_cap": true,
	"chat.prefill": true, "chat.prefill_cap": true, "chat.session.default_name": true,
	"chat.session.message": true, "chat.session.messages": true, "chat.tok_unit": true,
	"chat.tool.bash_lbl": true, "chat.tool.machines_list_lbl": true,
	"chat.tool.see_image_head": true, "chat.tool.see_image_lbl": true,
	"chat.tool.tracker_head": true, "chat.tool.tracker_lbl": true, "config.title": true,
	"export.format_heading": true, "export.format_json": true, "export.format_md": true,
	"internet.engine_crawl4ai": true, "mcpmodal.args_label": true,
	"mcpmodal.transport_label": true, "mcpmodal.transport_stdio": true,
	"mcpmodal.url_label": true, "memory.mode_auto": true, "memory.mode_label": true,
	"models.bench.button": true, "models.bench.total": true, "models.mem.label": true,
	"models.raw_config": true, "preset.backend_label": true, "preset.batch_label": true,
	"preset.destination_label": true, "preset.effort_opt_high": true,
	"preset.effort_opt_medium": true, "preset.effort_opt_xhigh": true,
	"preset.flash_label": true, "preset.minp_label": true,
	"preset.quant_auto_placeholder": true, "preset.quant_label": true,
	"preset.raw_heading": true, "preset.spec_opt_dflash": true,
	"preset.spec_opt_dspark": true, "preset.spec_opt_eagle3": true,
	"preset.spec_opt_mtp": true, "preset.threads_batch_label": true,
	"preset.threads_batch_sub": true, "preset.threads_sub": true, "preset.title": true,
	"preset.topk_label": true, "preset.topp_label": true, "preset.ubatch_label": true,
	"preset.vision_label": true, "presets.title": true,
	"projects.conversation_fallback": true, "projects.conversation_word": true,
	"projects.message_label": true, "projects.options": true, "projects.trackers": true,
	"reason.hint_max": true, "sessions.title": true, "settings.backup.auto_badge": true,
	"settings.backup.restore_done_title": true, "settings.bench_button": true,
	"settings.presets.quant_title": true, "status.path_presets": true,
	"task.freq_unit_minutes": true, "task.preset_heading": true,
	"task.script_heading": true, "task.type_heading": true, "tasks.active_label": true,
	"tasks.script_tag": true, "tasks.unit_minutes": true, "tracker.count_plural": true,
	"tracker.count_singular": true, "tracker.date_label": true, "tracker.modal_title": true,
	"tracker.options": true, "tracker.point_plural": true, "tracker.point_singular": true,
	"tracker.pt_abbrev": true, "uikit.confirm_title": true, "uikit.info_title": true,
	"uikit.ok": true,
}

// reWordy matches a value worth flagging when unchanged from fr: at least two
// letters, so pure punctuation/numbers/symbols (like " — maximum " fragments
// or "…") don't trigger false positives on their own — those are caught
// separately if they also contain real words.
var reWordy = regexp.MustCompile(`\p{L}{2,}`)

func main() {
	b, err := os.ReadFile(dataFile)
	if err != nil {
		fmt.Println("could not read", dataFile, ":", err)
		fmt.Println("(run this from the repository root)")
		os.Exit(2)
	}
	src := string(b)

	starts := reLangStart.FindAllStringSubmatchIndex(src, -1)
	if len(starts) == 0 {
		fmt.Println("no language blocks found — is the file format still var I18N = { lang: { ... }, ... } ?")
		os.Exit(2)
	}

	langs := map[string]map[string]bool{}
	values := map[string]map[string]string{}
	order := []string{}
	for i, m := range starts {
		name := src[m[2]:m[3]]
		blockStart := m[1]
		blockEnd := len(src)
		if i+1 < len(starts) {
			blockEnd = starts[i+1][0]
		}
		block := src[blockStart:blockEnd]
		keys := map[string]bool{}
		vals := map[string]string{}
		for _, e := range reEntry.FindAllStringSubmatch(block, -1) {
			keys[e[1]] = true
			vals[e[1]] = e[2]
		}
		langs[name] = keys
		values[name] = vals
		order = append(order, name)
	}

	fr, ok := langs["fr"]
	if !ok {
		fmt.Println("no 'fr' block found — fr is the reference language, every other block is checked against it")
		os.Exit(2)
	}
	frVals := values["fr"]
	fmt.Printf("Reference (fr): %d keys\n\n", len(fr))

	ok = true
	for _, lang := range order {
		if lang == "fr" {
			continue
		}
		keys := langs[lang]
		var missing, extra []string
		for k := range fr {
			if !keys[k] {
				missing = append(missing, k)
			}
		}
		for k := range keys {
			if !fr[k] {
				extra = append(extra, k)
			}
		}
		sort.Strings(missing)
		sort.Strings(extra)
		pct := 100.0
		if len(fr) > 0 {
			pct = 100.0 * float64(len(fr)-len(missing)) / float64(len(fr))
		}
		fmt.Printf("%s: %d/%d keys (%.0f%%)\n", lang, len(keys)-len(extra), len(fr), pct)
		if len(missing) > 0 {
			ok = false
			fmt.Printf("  missing %d key(s):\n", len(missing))
			for _, k := range missing {
				fmt.Printf("    %s\n", k)
			}
		}
		if len(extra) > 0 {
			ok = false
			fmt.Printf("  %d key(s) not in fr (typo in the key name?):\n", len(extra))
			for _, k := range extra {
				fmt.Printf("    %s\n", k)
			}
		}

		// Second pass: values present but identical to fr and not a known
		// loanword — likely just never translated. Warning only, not a
		// failure (see doc comment above).
		var suspicious []string
		langVals := values[lang]
		for k := range keys {
			if knownLoanwords[k] {
				continue
			}
			v, has := langVals[k]
			fv, hasFr := frVals[k]
			if has && hasFr && v == fv && reWordy.MatchString(v) {
				suspicious = append(suspicious, k)
			}
		}
		if len(suspicious) > 0 {
			sort.Strings(suspicious)
			fmt.Printf("  ⚠ %d value(s) identical to fr — probably never translated (not a loanword in tools/verify-i18n's list):\n", len(suspicious))
			for _, k := range suspicious {
				fmt.Printf("    %s = %q\n", k, langVals[k])
			}
		}
	}
	if !ok {
		fmt.Println("\nFAIL — see missing/extra keys above.")
		os.Exit(1)
	}
	fmt.Println("\nOK — every language has exactly the same keys as fr. Review any ⚠ warnings above by hand.")
}
