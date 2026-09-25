package ajean

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// relay_clock.go : horloge corrigée pour la fenêtre anti-rejeu E2E. Les
// requêtes du portail portent l'heure du navigateur (juste, en pratique) et
// sont refusées au-delà de ±90 s d'écart. Une machine dont l'horloge dérive
// (Windows sans synchro NTP) refusait donc TOUT en 403 : portail bloqué sur
// « démarrage », flux de chat coupé en route. On mesure l'écart avec le relais
// (en-tête Date, à la seconde près) et on le compense.

var (
	relayClockOffsetMs atomic.Int64 // heure du relais - heure locale
	relayClockOnce     sync.Once
	relayClockLast     atomic.Int64 // dernière mesure (ms locales), anti-rafale
)

// e2eNowMs : heure de référence pour la fenêtre E2E, en ms Unix.
func e2eNowMs() int64 {
	relayClockOnce.Do(func() {
		go func() {
			for {
				measureRelayClock()
				time.Sleep(10 * time.Minute)
			}
		}()
	})
	return time.Now().UnixMilli() + relayClockOffsetMs.Load()
}

// remeasureRelayClock relance une mesure (au plus toutes les 30 s), appelée
// quand un horodatage tombe hors fenêtre : l'horloge a peut-être bougé.
func remeasureRelayClock() {
	if time.Now().UnixMilli()-relayClockLast.Load() > 30_000 {
		go measureRelayClock()
	}
}

func measureRelayClock() {
	relayClockLast.Store(time.Now().UnixMilli())
	c := &http.Client{Timeout: 10 * time.Second}
	t0 := time.Now()
	resp, err := c.Head(relayHTTPBase() + "/")
	if err != nil {
		return
	}
	resp.Body.Close()
	t1 := time.Now()
	d, err := http.ParseTime(resp.Header.Get("Date"))
	if err != nil {
		return
	}
	// Date est tronquée à la seconde : +500 ms pour viser le milieu.
	mid := t0.Add(t1.Sub(t0) / 2)
	off := d.Add(500*time.Millisecond).UnixMilli() - mid.UnixMilli()
	// Écart sous 2 s : bruit de mesure, on garde l'horloge locale telle quelle.
	if off > -2000 && off < 2000 {
		off = 0
	}
	relayClockOffsetMs.Store(off)
}
