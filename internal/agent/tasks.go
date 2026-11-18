package agent

// Les tâches : ce que l'assistant fait tout seul, à une heure donnée ou
// régulièrement (paquet tasks). Une tâche travaille dans un fil ISOLÉ, avec
// tous les outils : elle ne voit aucune conversation et n'en bloque aucune,
// même si elle dure. Seul son compte rendu arrive dans une conversation, comme
// un message de l'assistant auquel l'utilisateur peut répondre.

import (
	"context"
	"errors"
	"strings"
	"time"

	"ajean/internal/config"
	"ajean/internal/llm"
	"ajean/internal/memory"
	"ajean/internal/tasks"
	"ajean/internal/tool"
)

const (
	maxTaskIterations = 40
	taskDeliveryWait  = 5 * time.Minute // attente d'une conversation occupée avant d'y livrer quand même
	taskTitle         = "Tâches"        // titre d'une conversation ouverte par un compte rendu
)

// taskPrompt : la consigne d'une tâche telle que le modèle la reçoit.
func taskPrompt(t tasks.Task, now time.Time) string {
	var b strings.Builder
	b.WriteString("[" + config.Stamp(now) + "] (Scheduled task \"" + t.Name + "\", running on your own. The user is not here and sees nothing of your work, only your final message.)\n\n")
	b.WriteString(t.Prompt)
	b.WriteString("\n\nDo what is needed with your tools, then write the message the user will receive: complete on its own (they do not have this task in front of them), in their language, short.")
	if t.Quiet {
		b.WriteString(" If there is nothing worth telling them, reply with exactly: " + tasks.Nothing)
	}
	return b.String()
}

// RunTask exécute une tâche du modèle et renvoie son compte rendu : le dernier
// message du modèle (ce qu'il a dit en chemin n'intéresse pas l'utilisateur).
func RunTask(ctx context.Context, t tasks.Task) (string, error) {
	if !config.Get().Chat().Ready() {
		return "", llm.ErrNotConfigured
	}
	turn := time.Now().UnixMilli()
	exec := func(tc llm.ToolCall) string {
		c, ok := decode(tc)
		if !ok {
			return tool.Err("arguments illisibles (JSON invalide)")
		}
		return allTools.run(ctx, c, t.Conv, turn, true)
	}
	msgs := []llm.Message{{Role: "user", Content: taskPrompt(t, time.Now())}}
	_, texts, err := loop(ctx, msgs, allTools.specs, maxTaskIterations, nil, exec)
	emit(event{Type: "review"}) // une tâche a pu créer ou modifier quelque chose
	if err != nil {
		return "", err
	}
	if len(texts) == 0 {
		return "", errors.New("le modèle n'a rien répondu")
	}
	return texts[len(texts)-1], nil
}

// DeliverTask remet le compte rendu d'une tâche à l'utilisateur : un message de
// l'assistant dans la conversation où la tâche a été créée (à défaut la plus
// récente, ou une nouvelle).
func DeliverTask(t tasks.Task, report string) {
	c := get(t.Conv)
	if c == nil {
		if l := List(); len(l) > 0 {
			c = get(l[0].ID)
		}
	}
	if c == nil {
		c = create()
	}
	now := time.Now()
	c.mu.Lock()
	// Un tour en cours travaille sur sa copie du contexte et la remet à la fin :
	// un message ajouté maintenant en disparaîtrait. On attend qu'il finisse.
	for deadline := now.Add(taskDeliveryWait); c.busy && time.Now().Before(deadline); {
		c.mu.Unlock()
		time.Sleep(time.Second)
		c.mu.Lock()
	}
	if c.title == "" {
		c.title = taskTitle
	}
	c.log = append(c.log, Item{Role: "assistant", Text: report, TS: now.UnixMilli(), Kind: kindTask})
	c.msgs = append(c.msgs, taskMessages(now, t.Name, report)...)
	dropped := c.trimLocked()
	c.saveLocked()
	c.mu.Unlock()
	c.summarizeLater(dropped)

	memory.Log("message", "Tâche « "+t.Name+" »\nAJEAN : "+clip(report, 400))
	emit(event{Type: "convs"})
	emit(event{Type: "done", Conv: c.id, Text: report, TS: now.UnixMilli(), Kind: kindTask})
}

// taskMessages : un compte rendu de tâche dans le contexte du modèle. Un
// message de l'assistant suit toujours un message « utilisateur » : certaines
// API l'exigent.
func taskMessages(at time.Time, name, report string) []llm.Message {
	note := "(Automatic, not written by the user: your scheduled task ran and you sent them the message below.)"
	if name != "" {
		note = "(Automatic, not written by the user: your scheduled task \"" + name + "\" ran and you sent them the message below.)"
	}
	return []llm.Message{
		{Role: "user", Content: "[" + config.Stamp(at) + "] " + note},
		{Role: "assistant", Content: report},
	}
}
