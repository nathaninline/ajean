package jean

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// cmdTest sanity-checks the LLM end-to-end: HTTP /health, then a minimal chat
// completion to confirm the model actually generates tokens.
func cmdTest(args []string) error {
	port := LLMPort()
	fmt.Printf("→ GET http://localhost:%d/health … ", port)
	if !healthCheck() {
		fmt.Println(red("ko"))
		return fmt.Errorf("/health ne répond pas — jean start d'abord")
	}
	fmt.Println(green("ok"))

	fmt.Printf("→ chat completion (prompt « ping ») … ")
	msgs := []Message{{Role: "user", Content: "Réponds juste « pong ». Rien d'autre."}}
	var reply strings.Builder
	t0 := time.Now()
	var firstTok time.Time
	tokens := 0
	_, err := runChat(context.Background(), msgs, 0, Caps{}, func(ev StreamEvent) bool {
		if ev.Content != "" {
			if firstTok.IsZero() {
				firstTok = time.Now()
			}
			tokens++
			reply.WriteString(ev.Content)
		}
		return true
	})
	if err != nil {
		fmt.Println(red("ko"))
		return err
	}
	elapsed := time.Since(t0)
	ttft := time.Duration(0)
	if !firstTok.IsZero() {
		ttft = firstTok.Sub(t0)
	}
	fmt.Println(green("ok"))

	out := strings.TrimSpace(reply.String())
	if len(out) > 120 {
		out = out[:120] + "…"
	}
	fmt.Printf("\n  %s  %s\n", cyan("réponse :"), out)
	fmt.Printf("  %s  %s\n", cyan("ttft    :"), ttft.Round(time.Millisecond))
	fmt.Printf("  %s  %s (%d tokens)\n", cyan("total   :"), elapsed.Round(time.Millisecond), tokens)
	if elapsed > 0 {
		tps := float64(tokens) / (elapsed - ttft).Seconds()
		if tokens > 0 && elapsed > ttft {
			fmt.Printf("  %s  %.1f tok/s\n", cyan("decode  :"), tps)
		}
	}
	if tokens == 0 {
		return fmt.Errorf("aucun token généré")
	}
	fmt.Println("\n" + green("[ok]") + " l'IA répond")
	return nil
}
