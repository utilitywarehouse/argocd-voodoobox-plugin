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
	reKeyName        = regexp.MustCompile(`#.*?argocd-voodoobox-plugin:\s*?(?P<keyName>\w+)`)
	reRepoURLWithSSH = regexp.MustCompile(`(?P<beginning>^\s*-\s*(?:ssh:\/\/)?)(?P<user>\w.+?@)?(?P<domain>\w.+?)(?P<repoDetails>[\/:].*$)`)
)

func setupGitSSH(ctx context.Context, cwd string, app applicationInfo) (string, error) {
	knownHostsFragment := `-o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no`

	sec, err := getSecret(ctx, app.destinationNamespace, app.gitSSHSecret)
	if err != nil {
		return "", fmt.Errorf("unable to get secret err:%v", err)
	}

	sshDir := filepath.Join(cwd, ".ssh")
	if err := os.Mkdir(sshDir, 0700); err != nil {
		return "", fmt.Errorf("unable to create ssh config dir err:%s", err)
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

		case keyName != "" && !reRepoURLWithSSH.MatchString(l):
			return nil, nil, fmt.Errorf("found key reference in comment but next remote base url is not a valid SSH URL")

		// referencing key is not mandatory since only 1 key can be used for all private base
		// case keyName == "" && reRepoAddressWithSSH.MatchString(l):
		// 	return nil, nil, fmt.Errorf("found remote base url with ssh protocol without referenced key comment above")

		case keyName != "" && reRepoURLWithSSH.MatchString(l):
			// If Key if found replace domain
			new, domain, err := replaceDomainWithConfigHostName(l, keyName)
			if err != nil {
				return nil, nil, fmt.Errorf("error parsing remote base url")
			}

			l = new
			keyedDomains[keyName] = domain

			keyName = ""
		}

		out = append(out, l...)
		out = append(out, "\n"...)
	}

	return keyedDomains, out, nil
}

func replaceDomainWithConfigHostName(original string, keyName string) (string, string, error) {
	sections := reRepoURLWithSSH.FindStringSubmatch(original)
	if len(sections) != 4 && len(sections) != 5 {
		return "", "", fmt.Errorf("error parsing remote base url")
	}

	// URL should be either ssh:// or git@domain.com
	// need to do check because in our regex both are optional
	if !strings.Contains(sections[reRepoURLWithSSH.SubexpIndex("beginning")], "ssh://") &&
		sections[reRepoURLWithSSH.SubexpIndex("user")] == "" {
		return "", "", fmt.Errorf("private remote URL should either contain ssh:// or user@ i.e. git@domain")
	}

	domain := sections[reRepoURLWithSSH.SubexpIndex("domain")]
	newURL := sections[reRepoURLWithSSH.SubexpIndex("beginning")] +
		sections[reRepoURLWithSSH.SubexpIndex("user")] +
		keyName + "_" + strings.ReplaceAll(domain, ".", "_") +
		sections[reRepoURLWithSSH.SubexpIndex("repoDetails")]

	return newURL, domain, nil
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

		host := keyName + "_" + strings.ReplaceAll(domain, ".", "_")
		hostFragments = append(hostFragments, fmt.Sprintf(hostFragment, host, domain, keyFilePath))
	}
	if len(hostFragments) == 0 {
		return nil, fmt.Errorf("keys are not referenced, please reference keys on remote base url in kustomize file")
	}

	return []byte(strings.Join(hostFragments, "\n")), nil
}
