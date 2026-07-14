# Topgrade integration

Status: implemented/documented for the private V0.3 RC; not part of the v0.2.0
release and not yet a publicly verified integration. Topgrade remains the
orchestration owner.

hukou deliberately does not execute Homebrew, npm, Cargo, mise, operating-system,
GUI, firmware, or driver updates. If you want one command that invokes several
independent managers, let [Topgrade](https://github.com/topgrade-rs/topgrade)
orchestrate them and add hukou as one custom command.

## Before enabling it

Use this configuration only with a V0.3 RC build that actually provides
`hukou outdated` and policy-aware `upgrade --all`; the current official v0.2.0
release does not contain the full trust-first interface. First inspect the
private RC verification report when it becomes available.

Review the hukou-owned scope without changing local state:

```bash
hukou outdated
```

`hukou upgrade --all` means **all entries explicitly adopted by hukou**. It
does not mean every application on the machine and it never takes ownership
away from another package manager.

## Configuration

Add this entry to the `[commands]` table in Topgrade's `topgrade.toml`:

```toml
[commands]
"hukou adopted CLI tools" = "hukou upgrade --all"
```

Topgrade currently searches `${XDG_CONFIG_HOME:-~/.config}/topgrade.toml`
before `${XDG_CONFIG_HOME:-~/.config}/topgrade/topgrade.toml`. If both exist,
edit the first one because it has priority.

Run `hukou outdated` on its own before your first combined upgrade. Then run
Topgrade normally. A hukou checksum, drift, network, or selection failure is
reported as a failed custom command; hukou does not silently hand that tool to
another manager.

This composition is sequential orchestration, not one atomic transaction
across the machine. A later manager may still run or fail according to
Topgrade's own configuration. Each manager keeps its own logs, repair rules,
rollback capability, and ownership boundary.

## Operational boundary

```text
Topgrade
├── Homebrew / npm / Cargo / mise / operating-system steps (owned by Topgrade)
└── hukou upgrade --all
    └── only binaries recorded in hukou's manifest
```

Rollback remains manager-specific. `hukou rollback` applies only to versions
that hukou retained for an adopted tool; it cannot roll back changes made by
Homebrew, Topgrade, the operating system, or another manager.

This guide does not authorize publishing a Topgrade plugin, changing the hukou
repository visibility, or creating a v0.3 release. It is local configuration
documentation for the private RC only.
