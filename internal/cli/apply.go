// The apply command: rewriting history and publishing the result.

package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/andornaut/ai-attributions/internal/gitexec"
	"github.com/andornaut/ai-attributions/internal/rewrite"
)

func apply(repo *gitexec.Repo, cfg config, refs []string, carried []target, changes map[string]rewrite.Change) error {
	if err := checkRewritable(repo, cfg); err != nil {
		return err
	}
	targets, err := collectTargets(repo, cfg, refs, carried)
	if err != nil {
		return err
	}
	if err := rewrite.Run(repo, refSpecs(cfg, targets), changes); err != nil {
		return err
	}
	resolveRewritten(repo, targets)
	reportRewritten(targets)

	publish := publishable(targets)
	if len(publish) == 0 {
		reportDonef("no ref moved, so there is nothing to push")
		return nil
	}
	// Read before reporting, so what is named as unleased is what the push
	// really cannot hold the remote to. Only a push uses the network.
	if cfg.push {
		if err := leaseRemote(repo, cfg.remote, publish); err != nil {
			return err
		}
	}
	reportUnleased(publish)

	if cfg.push {
		sayf("\npushing to %s\n", cfg.remote)
		if err := repo.Run(pushArgs(cfg.remote, publish)...); err != nil {
			return err
		}
		reportDonef("the history is rewritten and pushed to %s", cfg.remote)
		return nil
	}
	sayf("\nnot pushed. To publish the rewrite:\n\n    git %s\n",
		strings.Join(pushArgs(cfg.remote, publish), " "))
	reportDonef("the history is rewritten here, and not pushed")
	return nil
}

// refSpecs names the history git-filter-repo is pointed at. filter-repo
// re-exports everything it is given through git fast-export, which drops the
// gpgsig header, so handing it a ref rather than the range would give a signed
// commit below the base a new hash and fork the branch from what it was
// measured against.
func refSpecs(cfg config, targets []target) []string {
	specs := make([]string, 0, len(targets))
	for _, t := range targets {
		specs = append(specs, rangeSpec(cfg, t.ref))
	}
	return specs
}

// rangeSpec names one ref's history, bounded by the base when there is one. The
// rewrite and the count of what it moves ask this question, and answering it
// twice would let them disagree about what the run covers.
func rangeSpec(cfg config, ref string) string {
	if cfg.base == "" {
		return ref
	}
	return cfg.base + ".." + ref
}

func checkRewritable(repo *gitexec.Repo, cfg config) error {
	if err := rewrite.CheckAvailable(); err != nil {
		return err
	}
	// Checked before the rewrite rather than after, so a missing remote does
	// not leave a rewritten history with no way to publish it.
	if cfg.push && !repo.HasRemote(cfg.remote) {
		return fmt.Errorf("the repository has no remote named %q", cfg.remote)
	}
	isClean, err := repo.IsClean()
	if err != nil {
		return err
	}
	if !isClean {
		return errors.New("the working tree has uncommitted changes; commit or stash them first")
	}
	return nil
}

// collectTargets records where each ref pointed before the rewrite, along with
// the remote value to hold the push lease against. Carried tags are backed up
// with the refs in scope, so restore puts a repointed tag back too.
func collectTargets(repo *gitexec.Repo, cfg config, refs []string, carried []target) ([]target, error) {
	stamp := time.Now().UTC().Format("20060102T150405Z")
	targets := make([]target, 0, len(refs)+len(carried))
	for _, ref := range refs {
		targets = append(targets, target{ref: ref, publish: true})
	}
	targets = append(targets, carried...)

	for i, t := range targets {
		hash, err := repo.Resolve(t.ref)
		if err != nil {
			return nil, err
		}
		targets[i].hash = hash
		targets[i].lease = leaseFor(repo, cfg.remote, t.ref)

		if err := repo.UpdateRef(hash, backupPrefix+stamp+"/"+strings.TrimPrefix(t.ref, "refs/")); err != nil {
			return nil, err
		}
	}
	sayf("saved the pre-rewrite refs under %s%s/\n", backupPrefix, stamp)
	return targets, nil
}

// leaseFor returns the commit the remote-tracking ref points at, which is what
// the remote held when it was last fetched. The local ref is the wrong value to
// lease against, since it moves ahead of the remote on every local commit.
func leaseFor(repo *gitexec.Repo, remote, ref string) string {
	branch, ok := strings.CutPrefix(ref, "refs/heads/")
	if !ok {
		return ""
	}
	lease, err := repo.Resolve("refs/remotes/" + remote + "/" + branch)
	if err != nil {
		return ""
	}
	return lease
}

// resolveRewritten records where each ref ended up, so that what moved is read
// from the refs rather than inferred from the changes. A ref that cannot be
// resolved keeps an empty value and counts as unmoved, which leaves it out of
// the push.
func resolveRewritten(repo *gitexec.Repo, targets []target) {
	for i, t := range targets {
		if hash, err := repo.Resolve(t.ref); err == nil {
			targets[i].after = hash
		}
	}
}

// publishable returns the refs the push covers: the ones this run moved and
// owns. Pushing a ref that did not move would force a value this run did not
// produce, and a tag is forced without a lease to stop it.
func publishable(targets []target) []target {
	var publish []target
	for _, t := range targets {
		if t.publish && t.moved() {
			publish = append(publish, t)
		}
	}
	return publish
}

func unleasedRefs(targets []target) []string {
	var unleased []string
	for _, t := range targets {
		if t.unleased() {
			unleased = append(unleased, t.ref)
		}
	}
	return unleased
}

// leaseRemote fills in a lease for every ref with no remote-tracking
// counterpart to read one from, a tag for instance. --force-with-lease takes an
// expected value explicitly, so the push itself refuses a ref standing
// somewhere this run never saw, rather than a check it could move behind.
func leaseRemote(repo *gitexec.Repo, remote string, targets []target) error {
	refs := unleasedRefs(targets)
	if len(refs) == 0 {
		return nil
	}
	values, err := repo.RemoteValues(remote, refs)
	if err != nil {
		return err
	}

	for i, t := range targets {
		if !t.unleased() {
			continue
		}
		// Leased against the value the rewrite started from, not the one just
		// read: a lease naming what is already there agrees to overwrite it. A
		// ref the remote does not carry stays unleased and is created.
		if _, carried := values[t.ref]; carried {
			targets[i].lease = t.hash
		}
	}
	return nil
}

// pushArgs builds the push. A ref with a lease is held to it; one without is
// forced, there being nothing on the remote to overwrite.
func pushArgs(remote string, targets []target) []string {
	// --atomic so that a ref whose lease fails takes the rest of the push down
	// with it. Half a published rewrite is a branch on new history with a tag
	// still naming the old.
	args := []string{"push", "--atomic", remote}
	for _, t := range targets {
		if t.lease != "" {
			args = append(args, fmt.Sprintf("--force-with-lease=%s:%s", t.ref, t.lease))
		}
	}
	for _, t := range targets {
		if t.lease == "" {
			args = append(args, fmt.Sprintf("+%s:%s", t.ref, t.ref))
			continue
		}
		args = append(args, fmt.Sprintf("%s:%s", t.ref, t.ref))
	}
	return args
}
