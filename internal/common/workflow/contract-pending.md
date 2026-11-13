# Incompatible workflow-contract changes awaiting the next version bump

The workflow contract's version (`ContractVersion`, `contract.go`) moves
only on an incompatible change, at most once per release. A change is
compatible when every lane valid under the last release stays valid and
behaves the same, and the work items that release claimed keep running
(a new optional key whose default is the present behaviour, a new value of
a key): it never bumps the version and needs no drain — regenerate the
registry's golden file and its baseline in place
(`PF_WRITE_CONTRACT_GOLDEN=1 go test ./internal/common/workflow -run
'^TestRegistryGoldenPinsTheVersion$'`) and the lane schema
(`go generate ./internal/common/workflow`). Any other change is
incompatible: a key removed or renamed, a key's meaning or default changed,
a new required key, a check that refuses a lane it accepted, a value
removed, a capsule or hand-off format the work items in flight rely on.

Register an incompatible change in the change that makes it: append one
entry below, a level-two heading naming what changed, its body what the
migration takes — for user lanes, for the work items in flight, for the
deployment. The registry pin is red on an incompatible change the registry
dump shows (a removed key, a new required key, a removed value) while this
list has no entry; leave `ContractVersion` as it is.

The release reads the entries (every line starting with a level-two
heading marker). While there is one, the release is refused until a change
bumps `ContractVersion` by one — writing every entry's migration and the
drain into a new `docs/upgrade.md` section, regenerating the golden file,
the baseline and the lane schema, moving every built-in lane's `contract:`
— and empties this list, keeping this text. With no entry the release
neither bumps the version nor drains.
