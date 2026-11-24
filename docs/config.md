Configuration
=============

This document describes how to configure [actionlint](..) behavior.

Note that configuration file is optional. The author tries to keep configuration file as minimal as possible not to
bother users to configure behavior of actionlint. Running actionlint without configuration file would work fine in most
cases.

## Configuration file

Configuration file `actionlint.yaml` or `actionlint.yml` can be put in `.github` directory.

Note: If you're using [Super-Linter][], the file should be placed in a different directory. Please check the project's document.

```yaml
# Configuration related to self-hosted runner.
self-hosted-runner:
  # Labels of self-hosted runner in array of strings.
  labels:
    - linux.2xlarge
    - windows-latest-xl
    - linux-multi-gpu

# Configuration variables in array of strings defined in your repository or organization.
config-variables:
  - DEFAULT_RUNNER
  - JOB_NAME
  - ENVIRONMENT_STAGE

# Path-specific configurations.
paths:
  # Glob pattern relative to the repository root for matching files. The path separator is always '/'.
  # This example configures any YAML file under the '.github/workflows/' directory.
  .github/workflows/**/*.{yml,yaml}:
    # List of regular expressions to filter errors by the error messages.
    ignore:
      # Ignore the specific error from shellcheck
      - 'shellcheck reported issue in this script: SC2086:.+'
  # This pattern only matches '.github/workflows/release.yaml' file.
  .github/workflows/release.yaml:
    ignore:
      # Ignore errors from the old runner check. This may be useful for (outdated) self-hosted runner environment.
      - 'the runner of ".+" action is too old to run on GitHub Actions'

# Configuration for checking remote reusable workflow calls against local snapshots of the
# remote workflow files.
reusable-workflows:
  # Directory which contains snapshots of remote reusable workflow files, relative to the
  # repository root.
  snapshots-dir: .github/actionlint-workflows
  # Report remote reusable workflow calls which have no corresponding snapshot.
  # The default is false (such calls are silently skipped).
  strict: false
```

- `self-hosted-runner`: Configuration for your self-hosted runner environment.
  - `labels`: Label names added to your self-hosted runners as list of pattern. Glob syntax supported by [`path.Match`][pat]
    is available.
- `config-variables`: [Configuration variables][vars]. When an array is set, actionlint will check `vars` properties strictly.
  An empty array means no variable is allowed. The default value `null` disables the check.
- `paths`: Configurations for specific file path patterns. This is a mapping from a glob pattern and the corresponding
  configuration.
  - `{glob}`: A file path glob pattern to apply the configuration. The path separator is always '/'. It is matched to the
    relative path from the repository root. For example `.github/workflows/**/*.yaml` matches all the workflow files (with
    `.yaml` file extension). For the glob syntax, please read the [doublestar][] library's documentation.
    - `ignore`: The configuration to ignore (filter) the errors by the error messages. This is an array of regular
      expressions. When one of the patterns matches the error message, the error will be ignored. It's similar to the
      `-ignore` command line option.
- `reusable-workflows`: Configuration for checking remote reusable workflow calls
  (`owner/repo/path/to/workflow.yml@ref`) against local snapshots of the remote workflow files.
  - `snapshots-dir`: A directory path relative to the repository root which contains the snapshots. When this value
    is not set, remote reusable workflow calls are not checked (the default behavior). The directory must be inside
    the repository. See the [Check remote reusable workflow calls with snapshots](#remote-workflow-snapshots) section
    for the details.
  - `strict`: When this value is `true`, a remote reusable workflow call which has no corresponding snapshot is
    reported as an error. The default value `false` silently skips such calls. This value has no effect when
    `snapshots-dir` is not set.

<a id="remote-workflow-snapshots"></a>
## Check remote reusable workflow calls with snapshots

By default, actionlint cannot check remote reusable workflow calls like
`octo-org/shared-workflows/.github/workflows/build.yml@v1` since it never fetches anything from the network. Inputs,
secrets, and outputs of such calls are not checked, and `needs.<job_id>.outputs.<name>` is typed as an arbitrary
string map.

With the `reusable-workflows.snapshots-dir` configuration, you can put snapshots (copies) of the remote workflow
files in your repository so that actionlint checks the remote calls in the same way as local reusable workflow
calls.

### Snapshot directory layout

A snapshot for a workflow call `owner/repo/path/to/workflow.yml@ref` must be stored at:

```
<snapshots-dir>/<owner>/<repo>/<path/to/workflow.yml>@<ref>
```

For example, when `snapshots-dir` is `.github/actionlint-workflows` and your workflow calls
`octo-org/shared-workflows/.github/workflows/build.yml@v1`, the snapshot file must be:

```
.github/actionlint-workflows/octo-org/shared-workflows/.github/workflows/build.yml@v1
```

The file content is the workflow file as it is at the `v1` ref of the remote repository. The matching rules are:

- The owner and repository names are case-insensitive (GitHub treats them case-insensitively). Store the directory
  names in lower case.
- The workflow path is case-sensitive.
- The ref is matched exactly. A snapshot for `@v1` is never used for a call with `@v1.2.3` even though the tag `v1`
  may point to the same commit as `v1.2.3`. If you call the same workflow with different refs, store one snapshot
  per ref. No version relationship between refs is inferred.
- The `@` in the snapshot file name is the separator between the workflow path and the ref. The file name is legal
  on all major platforms including Windows, but some archive tools rewrite file names containing `@`. Generate the
  snapshots with plain file copies (see the example below) rather than going through such tools.

A snapshot can be generated with a plain download, for example:

```sh
mkdir -p .github/actionlint-workflows/octo-org/shared-workflows/.github/workflows
curl -sSfL https://raw.githubusercontent.com/octo-org/shared-workflows/v1/.github/workflows/build.yml \
  -o '.github/actionlint-workflows/octo-org/shared-workflows/.github/workflows/build.yml@v1'
```

Note that the snapshots are not updated automatically. When the remote workflow is updated, the snapshots need to
be updated as well.

### What is checked and what is not

When a snapshot exists for a remote workflow call, the following checks work in the same way as local reusable
workflow calls. Error messages for remote calls end with `the metadata was read from snapshot "..."` so that you
can tell the metadata came from a snapshot.

- Required inputs and undefined inputs at `with:`
- Types of input values respecting `type:` of each input definition
- Required secrets and undefined secrets at `secrets:` (skipped when `secrets: inherit` is used)
- Outputs of the called workflow. `needs.<job_id>.outputs.<name>` is strictly typed so a typo in the output name
  is reported

The following cases cannot be detected even with snapshots:

- The snapshot declares an output but the called workflow does not actually produce it on some execution paths.
  actionlint only looks at the `on.workflow_call.outputs` definitions.
- The snapshot is stale. The content at the ref may have changed after the snapshot was taken.

Snapshots are untrusted data. They are only parsed as workflow files and never executed. Snapshot files must be
regular files; symbolic links are rejected and reported.

### Strict mode

By default, a remote workflow call which has no snapshot is silently skipped (the same behavior as when
`snapshots-dir` is not set). Setting `reusable-workflows.strict` to `true` reports such calls so that you can
notice missing snapshots:

```
no snapshot was found for remote reusable workflow "octo-org/shared-workflows/.github/workflows/build.yml@v1". save the workflow file of the remote repository as ".github/actionlint-workflows/octo-org/shared-workflows/.github/workflows/build.yml@v1" to check this call
```

### Note on mixing actionlint versions

The configuration file is parsed non-strictly so older actionlint versions silently ignore the `reusable-workflows`
key. This means that when your team mixes old and new actionlint versions, the older versions report fewer errors
than the newer versions and the results may differ between CI and local environments. Please unify the actionlint
version in your team when you use this configuration.

## Generate the initial configuration

You don't need to write the first configuration file by your hand. `actionlint` command can generate a default configuration
with `-init-config` flag.

```sh
actionlint -init-config
vim .github/actionlint.yaml
```

---

[Checks](checks.md) | [Installation](install.md) | [Usage](usage.md) | [Go API](api.md) | [References](reference.md)

[Super-Linter]: https://github.com/super-linter/super-linter
[pat]: https://pkg.go.dev/path#Match
[vars]: https://docs.github.com/en/actions/learn-github-actions/variables
[doublestar]: https://github.com/bmatcuk/doublestar
