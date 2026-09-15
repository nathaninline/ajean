package ajean
import "testing"
func TestPassationNameUnique(t *testing.T){
  a := passationName()
  // créer la page puis demander à nouveau → doit être différent
  if err := MemAdd(a, "## Objectif\nx\n"); err != nil { t.Fatalf("MemAdd: %v", err) }
  b := passationName()
  if a == b { t.Fatalf("attendu un nom différent, obtenu %s deux fois", a) }
  t.Logf("première=%s suivante=%s", a, b)
  // cleanup
  _ = MemDelete(a)
}
