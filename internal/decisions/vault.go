package decisions

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"strings"
)

const sealName = "seal.key"
const ciphertextLimit = 1024

var errVault = errors.New("OpenAI connection storage is unavailable")

type vault struct{ directory string }

func credentialName(actor string) string {
	digest := sha256.Sum256([]byte(actor))
	return hex.EncodeToString(digest[:]) + ".enc"
}

func (v vault) read(actor string) (string, error) {
	root, err := os.OpenRoot(v.directory)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotConnected
	}
	if err != nil {
		return "", errVault
	}
	defer root.Close()
	encoded, err := readPrivate(root, credentialName(actor), ciphertextLimit)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotConnected
	}
	if err != nil {
		return "", errVault
	}
	aead, err := readSeal(root)
	if err != nil {
		return "", errVault
	}
	clear, err := aead.Open(nil, nil, encoded, []byte(actor))
	if err != nil {
		return "", errVault
	}
	return string(clear), nil
}

func (v vault) save(actor, credential string) error {
	if err := os.Mkdir(v.directory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return errVault
	}
	root, err := os.OpenRoot(v.directory)
	if err != nil {
		return errVault
	}
	defer root.Close()
	dir, err := root.Stat(".")
	if err != nil || dir.Mode().Perm()&0o077 != 0 {
		return errVault
	}
	aead, err := readSeal(root)
	if errors.Is(err, os.ErrNotExist) {
		// Publish the complete key at once. Another process initializing the
		// directory concurrently can win the link without changing the key.
		entries, listErr := fs.ReadDir(root.FS(), ".")
		if listErr != nil {
			return errVault
		}
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".enc") {
				// A damaged or partial restore must retain the existing seal.
				aead, err = readSeal(root)
				if err != nil {
					return errVault
				}
				break
			}
		}
		if aead == nil {
			key := make([]byte, 32)
			if _, err := rand.Read(key); err != nil {
				return errVault
			}
			err = publishPrivate(root, sealName, key, false)
			if err != nil && !errors.Is(err, os.ErrExist) {
				return errVault
			}
			aead, err = readSeal(root)
		}
	}
	if err != nil {
		return errVault
	}
	ciphertext := aead.Seal(nil, nil, []byte(credential), []byte(actor))
	if err := publishPrivate(root, credentialName(actor), ciphertext, true); err != nil {
		return errVault
	}
	return nil
}

func (v vault) remove(actor string) error {
	root, err := os.OpenRoot(v.directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errVault
	}
	defer root.Close()
	if err := root.Remove(credentialName(actor)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errVault
	}
	return nil
}

func readSeal(root *os.Root) (cipher.AEAD, error) {
	key, err := readPrivate(root, sealName, 32)
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errVault
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}

func readPrivate(root *os.Root, name string, limit int64) ([]byte, error) {
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > limit {
		return nil, errVault
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if int64(len(data)) > limit {
		return nil, errVault
	}
	return data, err
}

func publishPrivate(root *os.Root, name string, data []byte, replace bool) error {
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return err
	}
	temporary := ".write-" + hex.EncodeToString(entropy[:])
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer root.Remove(temporary)
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if replace {
		return root.Rename(temporary, name)
	}
	return root.Link(temporary, name)
}
