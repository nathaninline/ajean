package ajean

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// backend_gguf.go — lecture MINIMALE du format binaire GGUF : juste assez pour
// trouver le nombre de couches (block_count) d'un modèle, sans jamais toucher
// aux poids eux-mêmes (les tenseurs, potentiellement des dizaines de Go, vivent
// APRÈS la section de métadonnées et ne sont jamais lus ici). Utilisé par
// /api/models pour afficher le nombre de couches à côté du réglage NGL (issue
// #43 upstream : calibrer NGL sans saturer la VRAM suppose de savoir combien
// de couches le modèle a EN TOUT).
//
// Format (spec GGUF, stable depuis ggml/llama.cpp) : magic "GGUF" (4 octets),
// version (uint32), tensor_count, metadata_kv_count, puis metadata_kv_count
// paires clé/valeur typées. On lit ces paires SÉQUENTIELLEMENT — chaque type
// non désiré est explicitement "sauté" (skipValue) pour avancer correctement
// jusqu'à la section de tenseurs, qu'on n'atteint jamais : dès que
// general.architecture et <architecture>.block_count sont trouvées, ou que
// metadata_kv_count est épuisé, la lecture s'arrête.

// Types de valeur GGUF (ordre et valeurs fixés par le format, ne pas réordonner).
const (
	ggufTypeUint8   = 0
	ggufTypeInt8    = 1
	ggufTypeUint16  = 2
	ggufTypeInt16   = 3
	ggufTypeUint32  = 4
	ggufTypeInt32   = 5
	ggufTypeFloat32 = 6
	ggufTypeBool    = 7
	ggufTypeString  = 8
	ggufTypeArray   = 9
	ggufTypeUint64  = 10
	ggufTypeInt64   = 11
	ggufTypeFloat64 = 12
)

// ggufMagic = "GGUF" lu en little-endian comme uint32.
const ggufMagic = 0x46554747

// ggufReader lit des valeurs scalaires/tableaux GGUF depuis un flux, dans
// l'ordre — jamais de retour arrière (io.Reader, pas io.ReaderAt) : la section
// de métadonnées se lit toujours de bout en bout, pas besoin de mieux.
type ggufReader struct {
	r   io.Reader
	err error
}

func (g *ggufReader) read(v any) {
	if g.err != nil {
		return
	}
	g.err = binary.Read(g.r, binary.LittleEndian, v)
}

func (g *ggufReader) u8() uint8   { var v uint8; g.read(&v); return v }
func (g *ggufReader) u16() uint16 { var v uint16; g.read(&v); return v }
func (g *ggufReader) u32() uint32 { var v uint32; g.read(&v); return v }
func (g *ggufReader) u64() uint64 { var v uint64; g.read(&v); return v }
func (g *ggufReader) i8() int8    { var v int8; g.read(&v); return v }
func (g *ggufReader) i16() int16  { var v int16; g.read(&v); return v }
func (g *ggufReader) i32() int32  { var v int32; g.read(&v); return v }
func (g *ggufReader) i64() int64  { var v int64; g.read(&v); return v }

// str lit une chaîne GGUF (longueur uint64 puis octets bruts, non terminée
// par un zéro). Plafonnée à 1 Mio : une clé ou une valeur texte de métadonnée
// ne dépasse jamais ça en pratique — au-delà, fichier corrompu ou mal aligné,
// mieux vaut abandonner que d'allouer une longueur n'importe quoi lue d'un
// flux binaire qu'on a peut-être mal interprété.
func (g *ggufReader) str() string {
	n := g.u64()
	if g.err != nil {
		return ""
	}
	if n > 1<<20 {
		g.err = fmt.Errorf("chaîne GGUF anormalement longue (%d octets)", n)
		return ""
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(g.r, b); err != nil {
		g.err = err
		return ""
	}
	return string(b)
}

// skipValue lit et jette une valeur du type donné sans l'interpréter, pour
// avancer correctement jusqu'à la clé suivante. Récursif pour un tableau
// (type élément + compte + éléments) : un tableau de tableaux n'existe pas
// dans les fichiers GGUF réels, mais rien n'empêche techniquement le format
// de l'exprimer, donc autant le gérer plutôt que de planter dessus.
func (g *ggufReader) skipValue(typ uint32) {
	if g.err != nil {
		return
	}
	switch typ {
	case ggufTypeUint8, ggufTypeInt8, ggufTypeBool:
		g.u8()
	case ggufTypeUint16, ggufTypeInt16:
		g.u16()
	case ggufTypeUint32, ggufTypeInt32, ggufTypeFloat32:
		g.u32()
	case ggufTypeUint64, ggufTypeInt64, ggufTypeFloat64:
		g.u64()
	case ggufTypeString:
		g.str()
	case ggufTypeArray:
		elemTyp := g.u32()
		n := g.u64()
		for i := uint64(0); i < n && g.err == nil; i++ {
			g.skipValue(elemTyp)
		}
	default:
		g.err = fmt.Errorf("type de valeur GGUF inconnu: %d", typ)
	}
}

// intValue lit une valeur entière (n'importe quelle largeur GGUF, signée ou
// non) et la renvoie en int64 — block_count est toujours un petit entier
// positif, la largeur exacte déclarée par le fichier n'a pas d'importance ici.
// Toute autre catégorie (chaîne, tableau…) est sautée et vaut 0 : ne devrait
// jamais arriver pour block_count en pratique, mais mieux vaut ignorer que
// mal interpréter.
func (g *ggufReader) intValue(typ uint32) int64 {
	switch typ {
	case ggufTypeUint8:
		return int64(g.u8())
	case ggufTypeInt8:
		return int64(g.i8())
	case ggufTypeUint16:
		return int64(g.u16())
	case ggufTypeInt16:
		return int64(g.i16())
	case ggufTypeUint32:
		return int64(g.u32())
	case ggufTypeInt32:
		return int64(g.i32())
	case ggufTypeUint64:
		return int64(g.u64())
	case ggufTypeInt64:
		return g.i64()
	default:
		g.skipValue(typ)
		return 0
	}
}

// ggufBlockCount lit le nombre de couches (block_count) d'un modèle GGUF :
// clé "<architecture>.block_count" (ex. "qwen3.block_count",
// "llama.block_count"), où l'architecture elle-même vient de la clé
// "general.architecture". Suppose que general.architecture apparaît AVANT
// block_count dans le fichier — convention constante de l'écrivain GGUF
// officiel (llama.cpp / gguf-py), jamais vue autrement en pratique.
//
// Best-effort total, comme le reste de ce fichier : fichier absent, pas un
// GGUF, format inattendu ou tronqué → (0, false), jamais une erreur qui
// remonterait à l'appelant. C'est un confort d'affichage (calibrer NGL), pas
// un chemin critique — l'app doit rester utilisable même si cette info manque.
func ggufBlockCount(path string) (int, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	g := &ggufReader{r: f}

	if g.u32() != ggufMagic {
		return 0, false
	}
	version := g.u32()
	var kvCount uint64
	if version >= 2 {
		g.u64() // tensor_count — jamais utilisé, on n'atteint jamais cette section
		kvCount = g.u64()
	} else {
		g.u32() // tensor_count en v1 (32 bits)
		kvCount = uint64(g.u32())
	}

	arch := ""
	blockCount := int64(0)
	for i := uint64(0); i < kvCount && g.err == nil; i++ {
		key := g.str()
		typ := g.u32()
		switch {
		case key == "general.architecture" && typ == ggufTypeString:
			arch = g.str()
		case arch != "" && blockCount == 0 && key == arch+".block_count":
			blockCount = g.intValue(typ)
		default:
			g.skipValue(typ)
		}
	}
	if g.err != nil || blockCount <= 0 {
		return 0, false
	}
	return int(blockCount), true
}
