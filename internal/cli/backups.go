// The backups and restore commands.

package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/andornaut/ai-attributions/internal/gitexec"
)

func listBackups(repo *gitexec.Repo) error {
	listing, err := repo.Output("for-each-ref", "--format=%(refname) %(objectname:short)", backupPrefix)
	if err != nil {
		return err
	}
	if strings.TrimSpace(listing) == "" {
		sayf("no backups saved\n")
		return nil
	}
	for line := range strings.SplitSeq(strings.TrimSpace(listing), "\n") {
		ref, hash, _ := strings.Cut(line, " ")
		saved := strings.TrimPrefix(ref, backupPrefix)
		stamp, original, _ := strings.Cut(saved, "/")
		sayf("%s  refs/%s  %s\n", stamp, original, hash)
	}
	sayf("\nput one run back with: ai-attributions restore <timestamp>\n")
	return nil
}

func restoreBackup(repo *gitexec.Repo, stamp string) error {
	// Ref completion offers a trailing slash, which would build a prefix that
	// matches nothing.
	stamp = strings.Trim(stamp, "/")
	if stamp == "" {
		return errors.New("restore needs a backup timestamp; ai-attributions backups lists them")
	}

	prefix := backupPrefix + stamp + "/"
	listing, err := repo.Output("for-each-ref", "--format=%(refname) %(objectname)", prefix)
	if err != nil {
		return err
	}
	if strings.TrimSpace(listing) == "" {
		return fmt.Errorf("no backup saved under %s%s", backupPrefix, stamp)
	}

	restored := 0
	for line := range strings.SplitSeq(strings.TrimSpace(listing), "\n") {
		ref, hash, _ := strings.Cut(line, " ")
		saved, ok := strings.CutPrefix(ref, prefix)
		if !ok {
			return fmt.Errorf("%s is not under %s, so the ref to restore cannot be worked out", ref, prefix)
		}
		original := "refs/" + saved
		if err := repo.UpdateRef(hash, original); err != nil {
			return err
		}
		sayf("%s -> %s\n", original, gitexec.Short(hash))
		restored++
	}
	if restored == 0 {
		return fmt.Errorf("no backup saved under %s%s", backupPrefix, stamp)
	}
	sayf("\nrestored. A published rewrite still needs a force push to undo on the remote\n")
	return nil
}
