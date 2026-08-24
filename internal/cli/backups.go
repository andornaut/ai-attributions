// The backups, restore and clean commands, and the snapshot an apply saves.

package cli

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/andornaut/ai-attributions/internal/gitexec"
)

// savedRun is one run's backup: the timestamp it was saved under, and where
// each ref stood before that run rewrote it, keyed by the ref itself rather
// than by the ref it was saved as.
type savedRun struct {
	stamp string
	refs  map[string]string
}

// backup is the snapshot a run saved, and whether this run is the one that
// wrote it. A run whose refs already stand where an earlier run saved them
// reuses that run's backup, which is not this run's to take away again.
type backup struct {
	stamp string
	wrote bool
}

func listBackups(repo *gitexec.Repo) error {
	runs, err := savedRuns(repo)
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		sayf("no backups saved\n")
		return nil
	}
	for _, run := range runs {
		for _, ref := range run.sorted() {
			sayf("%s  %s  %s\n", run.stamp, ref, gitexec.Short(run.refs[ref]))
		}
	}
	sayf("\nput one run back with: ai-attributions restore <timestamp>\n")
	sayf("take them away with: ai-attributions clean, or clean --keep-last <n>\n")
	return nil
}

func restoreBackup(repo *gitexec.Repo, stamp string) error {
	// Ref completion offers a trailing slash, which would name a run nothing is
	// saved under.
	stamp = strings.Trim(stamp, "/")
	if stamp == "" {
		return errors.New("restore needs a backup timestamp; ai-attributions backups lists them")
	}
	runs, err := savedRuns(repo)
	if err != nil {
		return err
	}
	run, ok := findRun(runs, stamp)
	if !ok {
		return fmt.Errorf("no backup saved under %s%s", backupPrefix, stamp)
	}

	// In one update, as the run was saved in one: a restore that stopped part
	// way would put a branch back and leave the tag that moved with it where
	// the rewrite left it.
	if err := repo.UpdateRefs(run.refs); err != nil {
		return err
	}
	for _, ref := range run.sorted() {
		sayf("%s -> %s\n", ref, gitexec.Short(run.refs[ref]))
	}
	sayf("\nrestored. A published rewrite still needs a force push to undo on the remote\n")
	return nil
}

// cleanBackups is the clean command: one run where a timestamp names it, the
// newest KeepLast runs where --keep-last bounds them, and every backup the
// repository holds where neither says otherwise.
func cleanBackups(repo *gitexec.Repo, cfg Config, stamp string) error {
	runs, err := savedRuns(repo)
	if err != nil {
		return err
	}
	// Ref completion offers a trailing slash, as it does to restore. Read
	// before the listing is weighed, so that a timestamp naming no backup is a
	// failure whether the repository holds other backups or none at all.
	if stamp = strings.Trim(stamp, "/"); stamp != "" {
		return cleanOneRun(repo, runs, stamp)
	}
	if len(runs) == 0 {
		sayf("no backups saved\n")
		return nil
	}

	// Zero for the bare command, which takes every backup away.
	keep := max(cfg.KeepLast, 0)
	removed, err := pruneRuns(repo, runs, keep)
	if err != nil {
		return err
	}
	if removed == 0 {
		sayf("nothing to remove: %d %s saved, and --keep-last asks for %d\n",
			len(runs), plural(len(runs), "run", "runs"), keep)
		return nil
	}
	sayf("\nremoved %d of %d saved %s\n", removed, len(runs), plural(len(runs), "run", "runs"))
	return nil
}

// cleanOneRun takes away the backup one timestamp names, and reports a
// timestamp that names none as a failure rather than as nothing to do.
func cleanOneRun(repo *gitexec.Repo, runs []savedRun, stamp string) error {
	run, ok := findRun(runs, stamp)
	if !ok {
		return fmt.Errorf("no backup saved under %s%s", backupPrefix, stamp)
	}
	return removeRun(repo, run)
}

// saveBackup records where each ref stands before the rewrite moves it, and
// bounds what earlier runs left behind.
//
// Pruning happens here, at the start of a run, rather than once one has
// finished or published: a backup is what restore puts back after a rewrite
// that turned out to be wrong, and pushing that rewrite is what opens the
// window in which someone reaches for it. The next run is what takes it away.
func saveBackup(repo *gitexec.Repo, saving map[string]string) (backup, error) {
	runs, err := savedRuns(repo)
	if err != nil {
		return backup{}, err
	}

	// A run whose refs stand exactly where the newest backup saved them has
	// nothing new to record, so it reuses that one. Two copies of one snapshot
	// spend a slot of the bound below on a run that could not be told from it.
	if len(runs) > 0 && maps.Equal(runs[len(runs)-1].refs, saving) {
		reused := runs[len(runs)-1]
		sayf("the refs stand where %s%s/ saved them, so no second copy is written\n",
			backupPrefix, reused.stamp)
		// The reused snapshot is this run's, so the bound is over what is left
		// once it is set aside, as it is below. Counting it would leave a run
		// that reused a snapshot reaching one rewrite less far back than one
		// that wrote its own.
		if _, err := pruneRuns(repo, runs[:len(runs)-1], defaultKeepRuns); err != nil {
			return backup{}, err
		}
		return backup{stamp: reused.stamp}, nil
	}

	// Written in one update, so that a run is saved whole or not at all: a
	// snapshot missing the refs a failure interrupted reads like a complete one
	// to restore, which would put a branch back and leave the tag that moved
	// with it where the rewrite left it.
	stamp := freeStamp(runs, time.Now())
	writing := make(map[string]string, len(saving))
	for ref, hash := range saving {
		writing[backupRef(stamp, ref)] = hash
	}
	if err := repo.UpdateRefs(writing); err != nil {
		return backup{}, err
	}
	sayf("saved the pre-rewrite refs under %s%s/\n", backupPrefix, stamp)
	// The bound is over the runs that came before, and this run's own snapshot
	// sits above it until the next rewrite prunes to it again. Counting this
	// one against the bound would cost an earlier run for a snapshot that a
	// rewrite moving nothing then takes away again, leaving one fewer than the
	// bound and no way back to it.
	if _, err := pruneRuns(repo, runs, defaultKeepRuns); err != nil {
		return backup{}, err
	}
	return backup{stamp: stamp, wrote: true}, nil
}

// dropBackupIfUnused takes away the snapshot a run saved when the rewrite moved
// nothing, there being no history to put back. Only the snapshot this run
// wrote: a reused one belongs to the run that did write it, and that run moved
// something.
func dropBackupIfUnused(repo *gitexec.Repo, moved bool, saved backup) error {
	if moved || !saved.wrote {
		return nil
	}
	runs, err := savedRuns(repo)
	if err != nil {
		return err
	}
	run, ok := findRun(runs, saved.stamp)
	if !ok {
		return nil
	}
	if err := repo.DeleteRefs(refsOf(run)); err != nil {
		return err
	}
	sayf("\nno ref moved, so the backup saved above is taken away rather than kept as a copy of what is still there\n")
	return nil
}

// pruneRuns removes every run but the newest keep, and reports how many it
// removed. The runs are given rather than read, so a caller that has already
// listed them prunes the state it decided against.
func pruneRuns(repo *gitexec.Repo, runs []savedRun, keep int) (int, error) {
	keep = max(keep, 0)
	if len(runs) <= keep {
		return 0, nil
	}
	removed := 0
	for _, run := range runs[:len(runs)-keep] {
		if err := removeRun(repo, run); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// removeRun takes one run's saved refs away and says so, naming the run rather
// than every ref under it: what a backup is reached for is the run.
func removeRun(repo *gitexec.Repo, run savedRun) error {
	refs := refsOf(run)
	if err := repo.DeleteRefs(refs); err != nil {
		return err
	}
	sayf("removed %s (%d %s)\n", run.stamp, len(refs), plural(len(refs), "ref", "refs"))
	return nil
}

// savedRuns returns every backup this tool has saved, oldest first. It is the
// one reader of the namespace: everything that lists, restores, prunes or
// counts a backup works from what it returns, so the layout of a backup ref is
// written down here alone. The timestamps are fixed width and UTC, so ordering
// them as strings orders them by time.
func savedRuns(repo *gitexec.Repo) ([]savedRun, error) {
	listing, err := repo.Output("for-each-ref", "--format=%(refname) %(objectname)", backupPrefix)
	if err != nil {
		return nil, err
	}

	byStamp := map[string]map[string]string{}
	for line := range strings.SplitSeq(strings.TrimSpace(listing), "\n") {
		ref, hash, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		saved, ok := strings.CutPrefix(ref, backupPrefix)
		if !ok {
			continue
		}
		stamp, original, ok := strings.Cut(saved, "/")
		if !ok {
			continue
		}
		if byStamp[stamp] == nil {
			byStamp[stamp] = map[string]string{}
		}
		byStamp[stamp]["refs/"+original] = hash
	}

	runs := make([]savedRun, 0, len(byStamp))
	for _, stamp := range slices.Sorted(maps.Keys(byStamp)) {
		runs = append(runs, savedRun{stamp: stamp, refs: byStamp[stamp]})
	}
	return runs, nil
}

// findRun returns the run saved under a timestamp, for a caller that has been
// given one to act on rather than a run to act on.
func findRun(runs []savedRun, stamp string) (savedRun, bool) {
	for _, run := range runs {
		if run.stamp == stamp {
			return run, true
		}
	}
	return savedRun{}, false
}

// sorted returns the refs a run saved, in one order rather than a map's, so
// that two listings of one backup read the same way.
func (r savedRun) sorted() []string { return slices.Sorted(maps.Keys(r.refs)) }

// refsOf returns the backup refs one run holds, which are the refs it saved
// under its own timestamp rather than the refs it saved them for.
func refsOf(run savedRun) []string {
	refs := make([]string, 0, len(run.refs))
	for ref := range run.refs {
		refs = append(refs, backupRef(run.stamp, ref))
	}
	return refs
}

// backupRef is where one run saves one ref, which is the layout savedRuns reads
// back. Written down beside it, so the two cannot drift apart.
func backupRef(stamp, ref string) string {
	return backupPrefix + stamp + "/" + strings.TrimPrefix(ref, "refs/")
}

// freeStamp returns the timestamp to save a run under: the time now, moved past
// the newest run already saved where the clock has not got there yet. The
// stamps are a second apart at best, so two rewrites in one second would
// otherwise share one and the second would overwrite the first's refs where
// they name the same branch. Moving on rather than reusing the second also
// keeps the stamps ordered, which is what makes the last one the newest.
func freeStamp(taken []savedRun, now time.Time) string {
	stamp := now.UTC().Format(stampLayout)
	if len(taken) == 0 {
		return stamp
	}
	newest := taken[len(taken)-1].stamp
	if stamp > newest {
		return stamp
	}
	saved, err := time.Parse(stampLayout, newest)
	if err != nil {
		return stamp
	}
	return saved.Add(time.Second).Format(stampLayout)
}
