package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	hostFragment = `Host %s
    HostName %s
    IdentitiesOnly yes
    IdentityFile %s
    User git
`
	singleKeyHostFragment = `Host *
    IdentitiesOnly yes
    IdentityFile %s
    User git
`
)

var (
	reKeyName            = regexp.MustCompile(`#.*?argocd-voodoobox-plugin:\s*?(?P<keyName>\w+)`)
	reRepoAddressWithSSH = regexp.MustCompile(`(?P<beginning>^\s*-\s*ssh:\/\/)(?P<domain>\w.+?)(?P<repoDetails>\/.*$)`)
)

func setupGitSSH(ctx context.Context, cwd string, app applicationInfo) (string, error) {
	// Even when there is no git SSH secret defined, we still override the
	// git ssh command (pointing the key to /dev/null) in order to avoid
	// using ssh keys in default system locations and to surface the error
	// if bases over ssh have been configured.
	sshCmd := `GIT_SSH_COMMAND=ssh -q -F none -o IdentitiesOnly=yes -o IdentityFile=/dev/null -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no`
	knownHostsFragment := `-o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no`

	// if ssh secret name is not set skip setup
	if app.gitSSHSecret.name == "" {
		return sshCmd, nil
	}

	sec, err := getSecret(ctx, app.destinationNamespace, app.gitSSHSecret)
	if err != nil {
		return sshCmd, fmt.Errorf("unable to get secret err:%v", err)
	}

	sshDir := filepath.Join(cwd, ".ssh")
	if err := os.Mkdir(sshDir, 0700); err != nil {
		return sshCmd, fmt.Errorf("unable to create ssh config dir err:%s", err)
	}

	// keyFilePaths holds key name and path values
	var keyFilePaths = make(map[string]string)

	// write ssh data to ssh dir
	for k, v := range sec.Data {
		if k == "known_hosts" {
			if err := os.WriteFile(filepath.Join(sshDir, k), v, 0600); err != nil {
				return "", fmt.Errorf("unable to write known_hosts to temp file err:%s", err)
			}
			knownHostsFragment = fmt.Sprintf(`-o UserKnownHostsFile=%s/known_hosts`, sshDir)
			continue
		}
		// if key is not known_hosts then its assumed to be private keys
		kfn := filepath.Join(sshDir, k)
		// if the file containing the SSH key does not have a
		// newline at the end, ssh does not complain about it but
		// the key will not work properly
		if !bytes.HasSuffix(v, []byte("\n")) {
			v = append(v, byte('\n'))
		}
		keyFilePaths[k] = kfn
		if err := os.WriteFile(kfn, v, 0600); err != nil {
			return "", fmt.Errorf("unable to write key to temp file err:%s", err)
		}
	}

	keyedDomain, err := processKustomizeFiles(cwd)
	if err != nil {
		return "", fmt.Errorf("unable to updated kustomize files err:%s", err)
	}

	sshConfigFilename := filepath.Join(sshDir, "config")

	body, err := constructSSHConfig(keyFilePaths, keyedDomain)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(sshConfigFilename, body, 0600); err != nil {
		return "", err
	}

	return fmt.Sprintf(`GIT_SSH_COMMAND=ssh -q -F %s %s`, sshConfigFilename, knownHostsFragment), nil
}

// processKustomizeFiles finds all Kustomization files by walking the repo dir.
// For each Kustomization file, it will replace remote base host
func processKustomizeFiles(tmpRepoDir string) (map[string]string, error) {
	kFiles := []string{}
	keyedDomain := make(map[string]string)

	err := filepath.WalkDir(tmpRepoDir, func(path string, info fs.DirEntry, err error) error {
		if filepath.Base(path) == "kustomization.yaml" ||
			filepath.Base(path) == "kustomization.yml" ||
			filepath.Base(path) == "Kustomization" {
			kFiles = append(kFiles, path)
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	for _, k := range kFiles {
		in, err := os.Open(k)
		if err != nil {
			return nil, err
		}
		defer in.Close()

		kd, out, err := updateRepoBaseAddresses(in)
		if err != nil {
			return nil, err
		}
		if len(kd) > 0 {
			if err := os.WriteFile(k, out, 0600); err != nil {
				return nil, err
			}
		}
		for k, v := range kd {
			keyedDomain[k] = v
		}
	}
	return keyedDomain, nil
}

// updateRepoBaseAddresses will read given kustomize file line by line trying to find KA key
// comment `# argocd-voodoobox-plugin: key_foo`, we then attempt to replace the domain on
// the next line by injecting given key name into domain, resulting in
// `key_foo_github_com`. We must not use `.` - as it breaks Host matching in
// .ssh/config. it will return map of key and domains it replaced so that ssh config file can be updated
func updateRepoBaseAddresses(in io.Reader) (map[string]string, []byte, error) {
	keyedDomains := make(map[string]string)
	var out []byte

	scanner := bufio.NewScanner(in)
	keyName := ""
	for scanner.Scan() {
		l := scanner.Text()

		switch {

		case reKeyName.MatchString(l):
			// copy key
			s := reKeyName.FindStringSubmatch(l)
			if len(s) == 2 {
				keyName = s[reKeyName.SubexpIndex("keyName")]
			}

		case keyName != "" && !reRepoAddressWithSSH.MatchString(l):
			return nil, nil, fmt.Errorf("found key reference in comment but next remote base url doesn't contain ssh://")

		// referencing key is not mandatory since only 1 ssh key
		//  can be sued for all private base
		// case keyName == "" && reRepoAddressWithSSH.MatchString(l):
		// 	return nil, nil, fmt.Errorf("found remote base url with ssh protocol without referenced key comment above")

		case keyName != "" && reRepoAddressWithSSH.MatchString(l):
			// If Key if found replace domain
			sections := reRepoAddressWithSSH.FindStringSubmatch(l)
			if len(sections) != 4 {
				return nil, nil, fmt.Errorf("error parsing remote base url")
			}
			domain := sections[reRepoAddressWithSSH.SubexpIndex("domain")]
			keyedDomains[keyName] = domain

			l = sections[reRepoAddressWithSSH.SubexpIndex("beginning")] +
				keyName + "_" + strings.ReplaceAll(domain, ".", "_") +
				sections[reRepoAddressWithSSH.SubexpIndex("repoDetails")]

			keyName = ""
		}

		out = append(out, l...)
		out = append(out, "\n"...)
	}

	return keyedDomains, out, nil
}

func constructSSHConfig(keyFilePaths map[string]string, keyedDomain map[string]string) ([]byte, error) {
	if len(keyFilePaths) == 1 {
		for _, keyFilePath := range keyFilePaths {
			return []byte(fmt.Sprintf(singleKeyHostFragment, keyFilePath)), nil
		}
	}

	hostFragments := []string{}
	for keyName, domain := range keyedDomain {
		keyFilePath, ok := keyFilePaths[keyName]
		if !ok {
			return nil, fmt.Errorf("unable to find path for key:%s, please make sure all referenced keys are added to git ssh secret", keyName)
		}

		keyedDomain := keyName + "_" + strings.ReplaceAll(domain, ".", "_")
		hostFragments = append(hostFragments, fmt.Sprintf(hostFragment, keyedDomain, domain, keyFilePath))
	}
	if len(hostFragments) == 0 {
		return nil, fmt.Errorf("keys are not referenced, please reference keys on remote base url in kustomize file")
	}

	return []byte(strings.Join(hostFragments, "\n")), nil
}
