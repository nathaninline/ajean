package ajean

// Vignettes des images du fil (voir handleChatFile, paramètre thumb). Une
// vignette est calculée une fois par fichier et par taille, puis gardée en
// mémoire tant que le fichier ne change pas (date et taille).

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"os"
	"strconv"
	"sync"
	"time"

	_ "image/gif" // décodeurs enregistrés pour image.Decode
	_ "image/png"
)

const (
	thumbMaxSourceBytes = 40 << 20 // au-delà, pas de vignette : le client prendra l'original
	thumbCacheMax       = 300
)

type thumbEntry struct {
	mod  time.Time
	size int64
	data []byte
}

var (
	thumbMu    sync.Mutex
	thumbCache = map[string]thumbEntry{}
)

// chatThumb renvoie une vignette JPEG dont le grand côté vaut px (borné
// 64..1600). Erreur si le fichier n'est pas une image décodable ici (webp…) :
// le client se rabat alors sur l'original.
func chatThumb(path string, st os.FileInfo, px int) ([]byte, string, error) {
	if px < 64 {
		px = 64
	}
	if px > 1600 {
		px = 1600
	}
	if st.Size() > thumbMaxSourceBytes {
		return nil, "", fmt.Errorf("image trop lourde pour une vignette")
	}
	key := path + "\x00" + strconv.Itoa(px)
	thumbMu.Lock()
	if e, ok := thumbCache[key]; ok && e.mod.Equal(st.ModTime()) && e.size == st.Size() {
		thumbMu.Unlock()
		return e.data, "image/jpeg", nil
	}
	thumbMu.Unlock()

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "", fmt.Errorf("format non pris en charge pour une vignette")
	}
	if o := exifOrientation(raw); o >= 2 && o <= 8 {
		src = applyOrientation(src, o)
	}
	if b := src.Bounds(); b.Dx() > px || b.Dy() > px {
		src = scaleDownToMax(src, px)
	}
	// Fond blanc sous la transparence : en JPEG elle deviendrait noire.
	flat := image.NewRGBA(src.Bounds())
	draw.Draw(flat, flat.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(flat, flat.Bounds(), src, src.Bounds().Min, draw.Over)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, flat, &jpeg.Options{Quality: 78}); err != nil {
		return nil, "", err
	}
	data := buf.Bytes()

	thumbMu.Lock()
	if len(thumbCache) >= thumbCacheMax {
		for k := range thumbCache { // éviction grossière : la place compte plus que l'ordre
			delete(thumbCache, k)
			if len(thumbCache) < thumbCacheMax/2 {
				break
			}
		}
	}
	thumbCache[key] = thumbEntry{mod: st.ModTime(), size: st.Size(), data: data}
	thumbMu.Unlock()
	return data, "image/jpeg", nil
}
