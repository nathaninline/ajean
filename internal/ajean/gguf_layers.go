package ajean

// gguf_layers.go — nombre de couches d'un modèle, lu dans l'en-tête GGUF (#43).
// Sert à régler NGL en connaissance de cause : sans ce chiffre, on ne sait pas
// de combien baisser 999 pour laisser de la VRAM au contexte et au cache KV.
//
// Seules les métadonnées sont parcourues (quelques Mo au pire, à cause du
// vocabulaire du tokenizer), jamais les tenseurs.

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// Types de valeur GGUF (spécification ggml/docs/gguf.md).
const (
	ggufU8 = iota
	ggufI8
	ggufU16
	ggufI16
	ggufU32
	ggufI32
	ggufF32
	ggufBool
	ggufString
	ggufArray
	ggufU64
	ggufI64
	ggufF64
)

var ggufFixedSize = map[uint32]int64{
	ggufU8: 1, ggufI8: 1, ggufBool: 1, ggufU16: 2, ggufI16: 2,
	ggufU32: 4, ggufI32: 4, ggufF32: 4, ggufU64: 8, ggufI64: 8, ggufF64: 8,
}

// ggufBlockCount renvoie le nombre de blocs (couches répétées) du modèle.
// llama.cpp en offloade block_count + 1 (la couche de sortie en plus) : c'est
// la valeur de -ngl qui met tout le modèle sur le GPU.
func ggufBlockCount(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<16)
	le := binary.LittleEndian

	var head struct {
		Magic   [4]byte
		Version uint32
		Tensors uint64
		KVs     uint64
	}
	if err := binary.Read(r, le, &head); err != nil {
		return 0, err
	}
	if string(head.Magic[:]) != "GGUF" || head.Version < 2 {
		return 0, errors.New("pas un fichier GGUF v2+")
	}
	readStr := func() (string, error) {
		var n uint64
		if err := binary.Read(r, le, &n); err != nil {
			return "", err
		}
		if n > 1<<20 {
			return "", errors.New("chaîne GGUF trop longue")
		}
		b := make([]byte, n)
		_, err := io.ReadFull(r, b)
		return string(b), err
	}
	skipStr := func() error {
		var n uint64
		if err := binary.Read(r, le, &n); err != nil {
			return err
		}
		_, err := r.Discard(int(n))
		return err
	}
	readUint := func(t uint32) (uint64, error) {
		switch t {
		case ggufU8, ggufI8:
			v, err := r.ReadByte()
			return uint64(v), err
		case ggufU16, ggufI16:
			var v uint16
			err := binary.Read(r, le, &v)
			return uint64(v), err
		case ggufU32, ggufI32:
			var v uint32
			err := binary.Read(r, le, &v)
			return uint64(v), err
		case ggufU64, ggufI64:
			var v uint64
			err := binary.Read(r, le, &v)
			return v, err
		}
		return 0, fmt.Errorf("type GGUF %d non entier", t)
	}
	var skip func(t uint32) error
	skip = func(t uint32) error {
		if n, ok := ggufFixedSize[t]; ok {
			_, err := r.Discard(int(n))
			return err
		}
		switch t {
		case ggufString:
			return skipStr()
		case ggufArray:
			var et uint32
			var n uint64
			if err := binary.Read(r, le, &et); err != nil {
				return err
			}
			if err := binary.Read(r, le, &n); err != nil {
				return err
			}
			if sz, ok := ggufFixedSize[et]; ok {
				_, err := r.Discard(int(sz * int64(n)))
				return err
			}
			for i := uint64(0); i < n; i++ {
				if err := skip(et); err != nil {
					return err
				}
			}
			return nil
		}
		return fmt.Errorf("type GGUF %d inconnu", t)
	}

	arch := ""
	counts := map[string]uint64{} // <arch>.block_count rencontrés avant general.architecture
	for i := uint64(0); i < head.KVs; i++ {
		key, err := readStr()
		if err != nil {
			return 0, err
		}
		var t uint32
		if err := binary.Read(r, le, &t); err != nil {
			return 0, err
		}
		switch {
		case key == "general.architecture" && t == ggufString:
			if arch, err = readStr(); err != nil {
				return 0, err
			}
		case strings.HasSuffix(key, ".block_count"):
			v, err := readUint(t)
			if err != nil {
				return 0, err
			}
			counts[strings.TrimSuffix(key, ".block_count")] = v
		default:
			if err := skip(t); err != nil {
				return 0, err
			}
		}
		if arch != "" {
			if v, ok := counts[arch]; ok {
				return int(v), nil
			}
		}
	}
	return 0, errors.New("block_count absent des métadonnées")
}

// handleModelLayers (GET ?model=<nom ou chemin>) : couches du modèle, et la
// valeur de NGL qui le met entièrement sur le GPU. Sans paramètre : le modèle
// de la configuration active.
func handleModelLayers(w http.ResponseWriter, r *http.Request) {
	m := strings.TrimSpace(r.URL.Query().Get("model"))
	if m == "" {
		m = ReadConfig()["MODEL"]
	}
	if m == "" {
		sendJSON(w, 200, map[string]any{"ok": false, "error": "aucun modèle"})
		return
	}
	p, err := resolveServeModelPath(m)
	if err != nil {
		sendJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	n, err := ggufBlockCount(p)
	if err != nil {
		sendJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "layers": n, "ngl_max": n + 1})
}
