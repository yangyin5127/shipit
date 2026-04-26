# Shipit

A tiny deployment tool. like javascript `shipit`.

## Overview

- CLI style: `shipit <environment> <tasks...>`
- Default config file: `./shipit.yaml`
- Built-in tasks: `deploy`, `rollback`
- Custom tasks: defined in `default.tasks`

## Quick Start


```
go install https://github.com/yangyin5127/shipit
```

Run from the directory that contains `shipit.yaml`:

```bash
shipit production deploy
shipit production rollback

```

If you pass only an environment, the default task is `deploy`:

```bash
shipit production
```

## CLI

```bash
shipit <environment> <tasks...>
```

```bash
shipit production deploy
shipit production rollback

shipit --tasks
shipit --environments
shipit --shipitfile ./shipit.yaml production deploy
shipit --require ./override.yaml production deploy
```

## Options

```text
Usage: shipit <environment> <tasks...>

Options:

  -V, --version         output the version number
  --shipitfile <file>   Specify a custom shipitfile to use
  --require <files...>  Script required before launching Shipit
  --tasks               List available tasks
  --environments        List available environments
  -h, --help            output usage information
```

## Configuration

The tool reads `shipit.yaml` from the current working directory by default. You can override it with `--shipitfile`.

```yaml
environments:
  production:
    servers:
      - deploy@example.com
    deployTo: /srv/app
    branch: main

default:
  workspace: /tmp/app
  repositoryUrl: git@example.com:team/app.git
  keepReleases: 5
  deleteOnRollback: false
  dirToCopy: build
  published:
    production: >
      cd /srv/app/current
      && supervisorctl restart app
```

## Config Mapping

Compared with the original JavaScript `shipit` style:

- `environments.<name>` maps to environment blocks such as `production` and `sandbox`
- `default.workspace`, `repositoryUrl`, `ignores`, `keepReleases`, `deleteOnRollback`, `dirToCopy` map to the original `default` block
- `default.published` maps the `published` hook for each environment

## Built-in Tasks

### `deploy`

`deploy` does the following:

1. Clones the configured repository into a timestamped local workspace.
2. Checks out the configured branch during clone.
3. Uploads `dirToCopy` to `deployTo/releases/<timestamp>`.
4. Updates `deployTo/current` to point to the new release.
5. Removes old releases based on `keepReleases`.
6. Runs the configured `published` hook.

### `rollback`

`rollback` does the following:

1. Finds the current release from the `current` symlink.
2. Switches `current` back to the previous release.
3. Deletes the rolled-back release when `deleteOnRollback: true`.
4. Runs the configured `published` hook again.

`rollback` requires at least two releases on the remote host.

## Custom Tasks

Custom tasks are defined under `default.tasks` and executed remotely on every server in the target environment.

```yaml
default:
  tasks:
    pwd: pwd
    test: ls -lah /srv/app
```

Run them with:

```bash
shipit production pwd
```

## Directory Layout

Releases are stored like this:

```text
<deployTo>/
  current -> releases/<timestamp>
  releases/
    20260426010101/
    20260426020202/
```

## Logging

The CLI prints stage logs during deploy and rollback, including:

- selected environment and tasks
- workspace and release path
- remote directory creation
- file upload progress
- `current` symlink updates
- old release cleanup
- `published` hook execution
- SSH command summaries

## Notes

- `--require` can be passed multiple times
- existing `--require` files are validated before execution
- YAML files passed to `--require` are merged into the base config in order
- SSH authentication uses `~/.ssh/id_rsa`
- server format is `user@host`
- files are uploaded with `scp`
- custom tasks and hooks are executed over SSH
