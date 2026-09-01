package ajean

import (
	"encoding/json"
	"strings"
	"testing"
)

// Budget du préambule envoyé À CHAQUE TOUR : prompt système + schémas des outils.
// Il est payé sur toute la conversation et sort du contexte utile.
//
// Ce test existe pour empêcher le regonflement. Le prompt a déjà été raccourci
// une fois pour une raison de comportement (un préambule verbeux fait
// sur-raisonner les modèles à reasoning, qui finissent leur tour sans appeler
// d'outil), puis une seconde fois pour le coût. Les deux fois, il avait
// regrossi ligne par ligne, chacune paraissant anodine.
//
// Approximation 1 tok ≈ 4 caractères : suffisante pour une alerte, et sans
// dépendance à un tokenizer.
//
// Relevé une fois, volontairement, quand l'IA a reçu la capacité de gérer
// elle-même ses tâches planifiées (outils task_*) et de supprimer une page de
// mémoire (mem_delete) pour la garder rangée : cinq schémas d'outils en plus,
// ~600 tokens, ajout demandé et accepté. Le garde-fou reste là pour bloquer le
// regonflement LIGNE À LIGNE du prompt lui-même, qui, lui, doit rester court.
//
// Léger relèvement (8400 → 8700) pour les tâches « script seul » : task_create
// porte un paramètre 'script' en plus (un script du dossier scripts tourne sans
// modèle ni tokens). Pas d'outils script_* dédiés — l'IA écrit ses scripts avec
// write/bash dans le dossier scripts, la consigne vit dans le briefing machine.
//
// Relèvement (8700 → 9600) pour la mémoire longue de conversation : deux schémas
// en plus, recall (ramène un bloc archivé au compactage) et recall_search (le
// retrouve par mots-clés). C'est ce qui rend les conversations quasi infinies
// sans perdre le fil — ajout demandé et accepté. Le garde-fou reste là pour
// bloquer le regonflement LIGNE À LIGNE du prompt lui-même.
//
// Relèvement (9600 → 10800) pour le 3ᵉ type de mémoire : l'outil `suivi` (données
// datées qui s'accumulent — compteurs, relevés, journaux). UN seul schéma, et
// toute sa navigation par niveaux vit dans SES RÉPONSES, pas dans le prompt (une
// seule ligne de consigne : « ça va dans suivi, pas dans une note »). Ajout demandé
// et accepté ; il sort au contraire le suivi des notes .md, qui restent petites.
const promptCharBudget = 10800 // ~2700 tokens, tout allumé (agent+web+mémoire+suivi)

func TestSystemPromptStaysLean(t *testing.T) {
	caps := Caps{Agent: true, Internet: true, Mem: MemAlways}
	sp := baseSystemPrompt(caps)
	tb, err := json.Marshal(EnabledTools(caps))
	if err != nil {
		t.Fatal(err)
	}
	total := len(sp) + len(tb)
	t.Logf("prompt %d car (~%d tok) + outils %d car (~%d tok) = ~%d tok",
		len(sp), len(sp)/4, len(tb), len(tb)/4, total/4)
	if total > promptCharBudget {
		t.Fatalf("préambule à %d car (~%d tok), budget %d car (~%d tok).\n"+
			"Avant de relever le budget : les schémas d'outils pèsent le double du prompt,\n"+
			"et une consigne écrite dans un schéma n'a pas à être répétée dans le prompt.",
			total, total/4, promptCharBudget, promptCharBudget/4)
	}
}

// Le prompt ne doit pas redevenir un catalogue d'outils : leurs schémas partent
// dans la MÊME requête et les décrivent déjà un par un.
func TestSystemPromptDoesNotRelistTools(t *testing.T) {
	sp := baseSystemPrompt(Caps{Agent: true, Internet: true, Mem: MemAlways})
	for _, line := range strings.Split(sp, "\n") {
		l := strings.TrimSpace(line)
		if !strings.HasPrefix(l, "- ") {
			continue
		}
		// Une puce qui commence par un nom d'outil = un catalogue qui revient.
		for _, tool := range []string{"bash ", "write ", "edit ", "mem_", "web_"} {
			if strings.HasPrefix(l[2:], tool) {
				t.Fatalf("le prompt réénumère un outil : %q", l)
			}
		}
	}
}

// Sans agent ni mémoire, aucun préambule : un modèle de chat simple à qui on
// ordonne d'appeler des outils invente des appels textuels qui fuient dans la
// réponse.
func TestSystemPromptEmptyWithoutTools(t *testing.T) {
	if sp := baseSystemPrompt(Caps{Mem: MemOff}); sp != "" {
		t.Fatalf("préambule non vide sans outils : %q", sp)
	}
}
