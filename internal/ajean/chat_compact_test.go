package ajean

import (
	"strings"
	"testing"
)

// helpers pour construire des historiques de test lisibles.
func um(s string) Message { return Message{Role: "user", Content: s} }
func am(s string) Message { return Message{Role: "assistant", Content: s} }
func atc(name string) Message {
	return Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Function: ToolCallFunc{Name: name, Arguments: "{}"}}}}
}
func tm(s string) Message { return Message{Role: "tool", ToolCallID: "c1", Content: s} }

func TestCompactBoundsProtectsHead(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys"},
		um("premier"), am("r1"),
		um("q2"), am("r2"),
		um("q3"), am("r3"),
	}
	// budget minuscule → queue = juste le dernier tour, tête = system SEUL
	// (le 1er user n'est plus épinglé, cf. compactBounds).
	head, tail := compactBounds(msgs, 1)
	if head != 1 { // system uniquement
		t.Fatalf("head = %d, attendu 1 (system seul)", head)
	}
	// Frontière sûre = user ou assistant (jamais un `tool`, qui serait orphelin).
	if r := msgs[tail].Role; r != "user" && r != "assistant" {
		t.Fatalf("la queue doit démarrer sur un user ou un assistant, obtenu %q", r)
	}
	if tail <= head {
		t.Fatalf("torse vide (tail=%d head=%d) alors qu'il y a du milieu à compacter", tail, head)
	}
}

// La queue ne doit jamais démarrer entre un assistant+tool_calls et ses
// résultats `tool` : elle recule jusqu'à la frontière sûre précédente (user ou
// assistant), donc l'assistant part dans la queue AVEC ses résultats.
func TestCompactBoundsKeepsToolPairs(t *testing.T) {
	msgs := []Message{
		um("q1"), am("r1"),
		um("q2"), atc("bash"), tm("sortie longue"), am("r2"),
		um("q3"), am("r3"),
	}
	// budget moyen qui, sans le recul, couperait au milieu du tour outillé.
	_, tail := compactBounds(msgs, msgTokens(msgs[6])+msgTokens(msgs[7])+msgTokens(msgs[5])+1)
	if r := msgs[tail].Role; r != "user" && r != "assistant" {
		t.Fatalf("la queue démarre sur %q : frontière non sûre (orphelin tool possible)", r)
	}
	// Vérifie qu'aucun message `tool` de la queue n'a perdu son assistant parent.
	for i := tail; i < len(msgs); i++ {
		if msgs[i].Role == "tool" {
			if i == tail || (msgs[i-1].Role != "assistant" && msgs[i-1].Role != "tool") {
				t.Fatalf("message tool orphelin à l'index %d de la queue", i)
			}
		}
	}
}

// Régression : DEUXIÈME compaction à l'intérieur d'un même tour. Une longue
// boucle d'outils (recherche web : dix pages lues d'affilée) ne contient AUCUN
// message `user` — reculer jusqu'à un `user` faisait donc avaler toute la
// séquence par la queue, torse vide, compaction sans effet. La queue doit
// pouvoir démarrer sur un `assistant`, et les gros résultats d'outils du début
// doivent se retrouver dans le torse (donc résumés).
func TestCompactBoundsSplitsLongToolLoop(t *testing.T) {
	page := func(n int) Message {
		return tm("contenu de page web très long " + string(rune('a'+n)) + strings.Repeat("x", 400))
	}
	msgs := []Message{
		um("premier"), am("ok"), // head : 1er user protégé
		um("cherche des trucs"), // la dernière demande utilisateur du tour
	}
	for i := 0; i < 10; i++ {
		msgs = append(msgs, atc("web_read"), page(i))
	}
	head, tail := compactBounds(msgs, estimateTokens(msgs)/4)
	if tail <= head {
		t.Fatalf("torse vide (tail=%d head=%d) : la boucle d'outils n'est pas compactable", tail, head)
	}
	if r := msgs[tail].Role; r != "user" && r != "assistant" {
		t.Fatalf("la queue démarre sur %q : frontière non sûre", r)
	}
	// Aucun `tool` orphelin en queue.
	for i := tail; i < len(msgs); i++ {
		if msgs[i].Role == "tool" && (i == tail || (msgs[i-1].Role != "assistant" && msgs[i-1].Role != "tool")) {
			t.Fatalf("message tool orphelin à l'index %d de la queue", i)
		}
	}
	// Le torse doit bien contenir des résultats d'outils (c'est ce qui remplit
	// la fenêtre) — sinon la compaction ne libérerait rien.
	tools := 0
	for _, m := range msgs[head:tail] {
		if m.Role == "tool" {
			tools++
		}
	}
	if tools == 0 {
		t.Fatal("aucun résultat d'outil dans le torse : rien à gagner à compacter")
	}
}

// Régression : après compaction pendant une recherche web, la DEMANDE EN COURS
// doit encore figurer telle quelle dans l'historique. Sans ça, le seul message
// `user` restant était le tout premier de la conversation (épinglé en tête) et
// le modèle répondait à celui-là au lieu de continuer la recherche.
func TestCompactKeepsPendingRequest(t *testing.T) {
	// Isolation + stub réseau : sans ça ce test tape le VRAI llama-server local
	// (LLMPort en dur dans summarizeTranscript) et devient lent/instable dès
	// qu'un modèle tourne en parallèle sur cette machine — même défaut déjà
	// corrigé sur TestCompactArchivesBigBlock (chat_recall_test.go), repéré ici
	// en creusant un blocage de plusieurs dizaines de secondes sur `go test`. La
	// demande en cours survit à la compaction que le résumé aboutisse ou pas
	// (voir compactMessages : `pending` est réinjecté après `mid` dans les deux
	// cas), donc un résumé stubbé qui réussit ne change rien à ce que ce test
	// vérifie.
	testHome(t)
	summarizerStub(t, "Résumé de test : recherche des horaires de train pour Lyon, plusieurs pages web lues.")
	page := func(n int) Message {
		return tm("contenu de page web très long " + string(rune('a'+n)) + strings.Repeat("x", 400))
	}
	msgs := []Message{
		um("première question de la conversation"), am("ok"),
		um("cherche les horaires du train pour Lyon"),
	}
	for i := 0; i < 10; i++ {
		msgs = append(msgs, atc("web_read"), page(i))
	}
	out, _ := compactMessages(t.Context(), msgs, Caps{})
	found := false
	for _, m := range out {
		if m.Role == "user" && strings.Contains(msgText(m), "horaires du train") {
			found = true
		}
	}
	if !found {
		t.Fatal("la demande en cours a disparu de l'historique compacté")
	}
}

// Régression : ce qu'on envoie au RÉSUMEUR doit encore contenir les résultats
// d'outils. On effaçait les résultats (dégraissage) AVANT de résumer : le
// résumeur ne voyait que des marqueurs, le résumé ne pouvait donc porter aucune
// information trouvée, et l'IA relançait la même recherche après chaque
// compactage — sans jamais s'arrêter.
func TestSummaryInputKeepsToolFindings(t *testing.T) {
	fait := "le train de 14h12 part quai 3"
	torso := []Message{
		atc("web_read"),
		tm(fait + strings.Repeat(" blabla de remplissage", 300)),
	}
	// Même transformation que compactMessages avant l'appel au résumeur.
	forSummary := make([]Message, len(torso))
	for i, m := range torso {
		forSummary[i] = m
		if m.Role == "tool" {
			if r := []rune(msgText(m)); len(r) > compactToolSummaryLen {
				forSummary[i].Content = string(r[:compactToolSummaryLen]) + "\n[…suite coupée]"
			}
		}
	}
	tr := renderTranscript(forSummary)
	if !strings.Contains(tr, fait) {
		t.Fatal("le fait trouvé n'atteint pas le résumeur : le résumé sera vide d'information")
	}
	if strings.Contains(tr, compactPrunedMarker) {
		t.Fatal("le résumeur reçoit des marqueurs d'effacement au lieu du contenu")
	}
	if len([]rune(tr)) > 4000 {
		t.Fatalf("transcription non bornée (%d runes) : le résumeur va déborder", len([]rune(tr)))
	}
}

// Un résumé qui n'est qu'une (ou des) référence recall doit être considéré comme
// raté (régression du bug « recall:r7 » vu en test réel), un vrai résumé non.
func TestSummaryLooksEmpty(t *testing.T) {
	empty := []string{"", "  ", "recall:r7", "recall:r7 recall:r6", "- recall:r3\n- recall:r4", "recall(r7)"}
	for _, s := range empty {
		if !summaryLooksEmpty(s) {
			t.Fatalf("résumé %q aurait dû être jugé vide/raté", s)
		}
	}
	good := "L'utilisateur cherche les meilleurs restaurants de Saint-Jean-de-Védas ; trouvés Bouillon Popote 5.0 et Sushi Corner 4.8."
	if summaryLooksEmpty(good) {
		t.Fatalf("un vrai résumé en prose est jugé vide à tort")
	}
}

func TestEstimateTokensGrows(t *testing.T) {
	small := estimateTokens([]Message{um("court")})
	big := estimateTokens([]Message{um("un message nettement plus long que le précédent pour dépasser")})
	if big <= small {
		t.Fatalf("estimateTokens ne croît pas avec la taille: small=%d big=%d", small, big)
	}
}
