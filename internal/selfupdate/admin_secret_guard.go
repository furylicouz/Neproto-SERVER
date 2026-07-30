package selfupdate

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxAdminSecretBytes = 512

type adminSecretSnapshot struct {
	path    string
	content []byte
	mode    os.FileMode
	owner   fileOwner
}

func captureAdminSecret(root string) (*adminSecretSnapshot, error) {
	secretPath := filepath.Join(root, "etc", "neproto", "web-admin.secret")
	info, err := os.Lstat(secretPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() < 32 || info.Size() > maxAdminSecretBytes {
		return nil, errors.New("invalid administrator secret file")
	}
	content, err := readBoundedAdminSecret(secretPath)
	if err != nil {
		return nil, err
	}
	normalized := strings.TrimSpace(string(content))
	if len(normalized) < 32 || len(normalized) > 256 || strings.ContainsAny(normalized, "\r\n\x00") {
		return nil, errors.New("invalid administrator secret")
	}
	return &adminSecretSnapshot{
		path: secretPath, content: append([]byte(nil), content...), mode: info.Mode().Perm(), owner: ownerFromFileInfo(info),
	}, nil
}

func (snapshot *adminSecretSnapshot) restoreIfChanged() (bool, error) {
	if snapshot == nil {
		return false, nil
	}
	current, err := readBoundedAdminSecret(snapshot.path)
	if err == nil && bytes.Equal(current, snapshot.content) {
		return false, nil
	}
	directory := filepath.Dir(snapshot.path)
	temporary, err := os.CreateTemp(directory, ".web-admin.restore-*")
	if err != nil {
		return false, err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err = temporary.Write(snapshot.content); err == nil {
		err = temporary.Sync()
	}
	closeErr := temporary.Close()
	if err != nil {
		return false, err
	}
	if closeErr != nil {
		return false, closeErr
	}
	if err := applyFileOwner(temporaryPath, snapshot.owner); err != nil {
		return false, err
	}
	if err := os.Chmod(temporaryPath, snapshot.mode); err != nil {
		return false, err
	}
	if err := os.Rename(temporaryPath, snapshot.path); err != nil {
		return false, err
	}
	verified, err := readBoundedAdminSecret(snapshot.path)
	if err != nil {
		return false, err
	}
	if !bytes.Equal(verified, snapshot.content) {
		return false, errors.New("administrator secret recovery verification failed")
	}
	return true, nil
}

func readBoundedAdminSecret(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() < 32 || info.Size() > maxAdminSecretBytes {
		return nil, errors.New("invalid administrator secret file")
	}
	content, err := io.ReadAll(io.LimitReader(file, maxAdminSecretBytes+1))
	if err != nil {
		return nil, err
	}
	if len(content) < 32 || len(content) > maxAdminSecretBytes {
		return nil, errors.New("invalid administrator secret file")
	}
	return content, nil
}
