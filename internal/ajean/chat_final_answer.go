package ajean

import "context"

// chat_final_answer.go — une réponse finale plutôt qu'un tour qui s'éteint.
//
// La boucle d'outils n'a pas de plafond d'étapes (choix assumé, voir runChat) :
// le seul cas où un tour finit sans un mot est celui du modèle qui ne produit ni
// réponse ni appel d'outil, relances comprises (« pensé sans agir »). L'utilisateur
// lisait alors « le modèle n'a pas produit de réponse », alors que le tour avait
// souvent déjà enchaîné des outils et qu'il y avait de quoi répondre.
//
// Avant d'en arriver là, on demande une dernière réponse SANS outils : ce qui a
// été trouvé ou fait, ce qui bloque, ce qu'il faudrait essayer. La consigne ne
// part que dans la requête de ce tour, jamais dans le fil gardé : seule la
// réponse y entre.

// finalAnswerNote : la consigne de la dernière demande. Rôle user comme les
// autres relances (un system en fin de séquence serait remonté en tête par
// normalizeSystemMessages, ce qui ferait relire tout le fil).
const finalAnswerNote = "[System: you did not answer. Do not call any tool. " +
	"Answer the user now, in their language, with what you already have: what you found or did, " +
	"what is still blocking and why, and what you would need or try next.]"

// wantFinalAnswer : la dernière demande a-t-elle un sens ? Une fois seulement
// (elle coupe les outils, donc ne se représente pas), jamais sans outils (rien
// n'a pu être fait) ni pendant une révision silencieuse de la mémoire, où ne
// rien dire est une issue normale.
func wantFinalAnswer(ctx context.Context, tools int, toolsDisabled bool) bool {
	return tools > 0 && !toolsDisabled && !isJeanReflecting(ctx)
}
