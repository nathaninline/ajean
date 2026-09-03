package ajean

import "testing"

func TestRepetitionGuardIgnoresDistinctSentences(t *testing.T) {
	var g repetitionGuard
	sentences := []string{
		"I'm going to read the first ten bytes of the header to check the magic number.",
		"That confirms it's a valid GGUF file, version three as expected.",
		"Now let me look at the tensor count and the metadata key-value count.",
		"The architecture key says qwen35, which matches what I saw earlier.",
	}
	for _, s := range sentences {
		if looping, _ := g.feed(s + " "); looping {
			t.Fatalf("distinct sentence wrongly flagged as a loop: %q", s)
		}
	}
}

func TestRepetitionGuardTriggersAtThreshold(t *testing.T) {
	var g repetitionGuard
	sentence := "I'm realizing the atlas layout is more complex than a simple grid, glyphs have varying heights and positions."
	for i := 1; i < repetitionGuardThreshold; i++ {
		if looping, _ := g.feed(sentence + " "); looping {
			t.Fatalf("triggered too early, on repeat #%d (threshold is %d)", i, repetitionGuardThreshold)
		}
	}
	looping, got := g.feed(sentence + " ")
	if !looping {
		t.Fatalf("expected loop detection on repeat #%d, got none", repetitionGuardThreshold)
	}
	if got == "" {
		t.Fatal("expected the detected sentence to be returned, got empty string")
	}
}

func TestRepetitionGuardIgnoresShortFiller(t *testing.T) {
	var g repetitionGuard
	// "Let me check." et "OK, next." reviennent légitimement dans un raisonnement
	// normal sans jamais signaler un blocage — repetitionGuardMinLen doit les
	// écarter même répétées bien plus de repetitionGuardThreshold fois.
	for i := 0; i < 10; i++ {
		if looping, _ := g.feed("Let me check. "); looping {
			t.Fatalf("short filler sentence wrongly flagged as a loop on iteration %d", i)
		}
	}
}

func TestRepetitionGuardNormalizesWhitespaceAndCase(t *testing.T) {
	var g repetitionGuard
	variants := []string{
		"This is a fairly long sentence about the font atlas layout that repeats.",
		"THIS IS A FAIRLY LONG SENTENCE ABOUT THE FONT ATLAS LAYOUT THAT REPEATS.",
		"This   is  a fairly long   sentence about the font atlas layout that repeats.",
	}
	for i, v := range variants {
		looping, _ := g.feed(v + " ")
		if i < repetitionGuardThreshold-1 && looping {
			t.Fatalf("triggered too early on variant %d", i)
		}
		if i == repetitionGuardThreshold-1 && !looping {
			t.Fatalf("case/whitespace variants weren't recognized as the same sentence")
		}
	}
}

// TestRepetitionGuardOnRealLoopTranscript rejoue (en abrégé) le motif observé
// dans un vrai blocage capturé en production : la même phrase quasi identique
// revenait des dizaines de fois dans le raisonnement d'un modèle coincé sur un
// déchiffrage d'atlas de police. Vérifie que le détecteur aurait coupé net à
// la 3e occurrence plutôt que de laisser filer jusqu'à la 82e comme ce jour-là.
func TestRepetitionGuardOnRealLoopTranscript(t *testing.T) {
	var g repetitionGuard
	real := "I'm realizing the atlas might not follow a simple row-based structure—band1 has 16 glyphs while band0 has 8, which breaks the pattern I was assuming."
	other := "This suggests the font atlas uses a bin-packing algorithm that groups glyphs by height rather than following a fixed grid."
	// Le vrai transcript alternait deux phrases qui revenaient chacune ~80 fois ;
	// on ne rejoue que les 5 premiers tours pour vérifier le déclenchement précoce.
	turns := []string{real, other, real, other, real}
	triggeredAt := -1
	for i, s := range turns {
		if looping, _ := g.feed(s + " "); looping {
			triggeredAt = i
			break
		}
	}
	// real apparaît aux index 0, 2, 4 (1re, 2e, 3e occurrence) — le déclenchement
	// attendu est donc à l'index 4, pas 2 : la garde compte par phrase normalisée,
	// pas par position, donc "other" intercalé entre les occurrences ne la trompe
	// pas (mais ne l'accélère pas non plus).
	if triggeredAt != 4 {
		t.Fatalf("expected the guard to trigger on the 3rd occurrence of `real` (index 4), got index %d", triggeredAt)
	}
}
