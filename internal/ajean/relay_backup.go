package ajean

// relay_backup.go — client HTTP des sauvegardes vers le relais ajean.link. Le
// blob est DÉJÀ chiffré (voir backup_bundle.go) : ce fichier ne fait que le
// transporter, authentifié par la clé de liaison de l'abonné.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// relayHTTPBase déduit l'URL HTTPS du relais depuis l'endpoint WebSocket
// (wss://ajean.link/agent → https://ajean.link). Surchargée par AJEAN_LINK_URL.
func relayHTTPBase() string {
	u := relayURL() // wss://host/agent
	u = strings.TrimSuffix(u, "/agent")
	u = strings.Replace(u, "wss://", "https://", 1)
	u = strings.Replace(u, "ws://", "http://", 1)
	return strings.TrimRight(u, "/")
}

func backupHTTPClient() *http.Client { return &http.Client{Timeout: 60 * time.Second} }

func backupAuthReq(method, url string, body io.Reader) (*http.Request, error) {
	tok := readLinkToken()
	if tok == "" {
		return nil, fmt.Errorf("aucune clé de liaison — l'accès distant ajean.link n'est pas configuré")
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	return req, nil
}

// BackupVersion : métadonnées d'une sauvegarde stockée sur le relais.
type BackupVersion struct {
	ID   string `json:"id"`
	Size int64  `json:"size"`
	When string `json:"when"`
}

// relayBackupList récupère la liste des sauvegardes de ce compte.
func relayBackupList() ([]BackupVersion, error) {
	req, err := backupAuthReq(http.MethodGet, relayHTTPBase()+"/backup", nil)
	if err != nil {
		return nil, err
	}
	resp, err := backupHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusPaymentRequired {
		return nil, fmt.Errorf("abonnement ajean.link requis")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("relais: %s", resp.Status)
	}
	var out struct {
		Versions []BackupVersion `json:"versions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Versions, nil
}

// relayBackupUpload envoie un blob chiffré et renvoie l'id attribué.
func relayBackupUpload(blob []byte) (string, error) {
	req, err := backupAuthReq(http.MethodPut, relayHTTPBase()+"/backup", bytes.NewReader(blob))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := backupHTTPClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusPaymentRequired {
		return "", fmt.Errorf("abonnement ajean.link requis")
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("relais: %s (%s)", resp.Status, strings.TrimSpace(string(b)))
	}
	var out struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out.ID, nil
}

// relayBackupDownload récupère un blob chiffré par son id.
func relayBackupDownload(id string) ([]byte, error) {
	req, err := backupAuthReq(http.MethodGet, relayHTTPBase()+"/backup/"+id, nil)
	if err != nil {
		return nil, err
	}
	resp, err := backupHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("relais: %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// --- Orchestration haut niveau -----------------------------------------------

// backupAutoEnabled : la sauvegarde automatique est activée (réglage BACKUP_AUTO).
func backupAutoEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(ReadConfig()["BACKUP_AUTO"])) {
	case "1", "on", "true", "yes", "oui":
		return true
	}
	return false
}

// RunBackupNow construit et envoie une sauvegarde. AUCUN mot de passe : le corps
// est chiffré avec la DEK de la mémoire (déjà en RAM) et le keyvault voyage en
// entête, si bien que la restauration ne réclame que la clé d'API. Exige donc que
// la mémoire soit chiffrée ET déverrouillée. Le relais ne voit qu'un blob opaque.
func RunBackupNow() (string, error) {
	dek, err := currentDEK()
	if err != nil {
		return "", fmt.Errorf("mémoire verrouillée — ouvre-la avant de sauvegarder")
	}
	v, _ := loadVault()
	if v == nil {
		return "", fmt.Errorf("active le chiffrement avant d'utiliser la sauvegarde")
	}
	tarData, err := buildBundleTar()
	if err != nil {
		return "", fmt.Errorf("construction du paquet : %w", err)
	}
	blob, err := buildBackupBlob(v, dek, tarData)
	if err != nil {
		return "", err
	}
	id, err := relayBackupUpload(blob)
	if err != nil {
		return "", err
	}
	_ = putStr(bkState, "backup_last", time.Now().UTC().Format(time.RFC3339))
	return id, nil
}

// StartBackupScheduler lance la boucle de sauvegarde automatique (une par jour).
// Ne fait rien tant que : l'auto n'est pas activée, l'accès distant n'est pas
// configuré, ou la mémoire n'est pas déverrouillée (pas de DEK). Idempotent.
var backupSchedOnce sync.Once

func StartBackupScheduler() {
	backupSchedOnce.Do(func() {
		go func() {
			t := time.NewTicker(1 * time.Hour)
			defer t.Stop()
			for range t.C {
				if !backupAutoEnabled() || !backupLinked() || !memUnlocked() {
					continue
				}
				// Une par jour : on saute si la dernière a moins de 24 h.
				if last := backupLast(); last != "" {
					if ts, err := time.Parse(time.RFC3339, last); err == nil && time.Since(ts) < 24*time.Hour {
						continue
					}
				}
				_, _ = RunBackupNow()
			}
		}()
	})
}

// RestoreBackup télécharge et restaure une sauvegarde. secret = la clé d'API (ou
// la clé de récupération) qui ouvre le keyvault de l'entête.
func RestoreBackup(id, secret string) error {
	blob, err := relayBackupDownload(id)
	if err != nil {
		return err
	}
	tarData, err := openBackupBlob(blob, secret)
	if err != nil {
		return err
	}
	return restoreBundleTar(tarData)
}

// backupLast renvoie l'horodatage de la dernière sauvegarde réussie (ou "").
func backupLast() string { return getStr(bkState, "backup_last") }

// backupLinked indique si un accès distant est configuré (prérequis sauvegarde).
func backupLinked() bool { return readLinkToken() != "" }
