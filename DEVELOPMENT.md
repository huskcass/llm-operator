## Getting Started

### Prerequisites
- go version v1.24.6+
- docker version 17.03+.
- kubectl version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.

### To Deploy on the cluster
**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/llmscaleoperator:tag
```

**NOTE:** This image ought to be published in the personal registry you specified.
And it is required to have access to pull the image from the working environment.
Make sure you have the proper permission to the registry if the above commands don’t work.

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/llmscaleoperator:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin
privileges or be logged in as admin.

**Create instances of your solution**
You can apply the samples (examples) from the config/sample:

```sh
kubectl apply -k config/samples/
```

>**NOTE**: Ensure that the samples has default values to test it out.

### To Uninstall
**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

## Project Distribution

The operator installs two ways: with kustomize from `config/default`, or with the Helm chart in `dist/chart`, which is generated from `config/`.

### Source of truth

Edit `config/`. Never edit `dist/`.

- **Hand-maintained:** everything under `config/` except the two generated paths below, e.g. `config/manager/manager.yaml`, `config/default`,
  `config/samples` and the scaffolded roles in `config/rbac`.
- **Generated in `config/`:** `config/crd/bases` and `config/rbac/role.yaml`, written by `make manifests` from the Go types and markers.
- **Generated in `dist/`:** `dist/install.yaml` and `dist/chart`. To change them, change `config/` and regenerate.
  `dist/install.yaml` is an intermediate artifact: the Helm plugin reads it to build the chart. Never install from it.

```
api/**/*_types.go + markers ── make manifests ──▶ config/crd/bases, config/rbac/role.yaml
                                                          │
config/manager, config/default, config/rbac, ...  ────────┤   hand-maintained
                                                          ▼
                              kustomize build config/default ──▶ dist/install.yaml
                                                                        │
                                      kubebuilder edit --plugins=helm/v2-alpha --force
                                                                        ▼
                                                                   dist/chart
```

### Regenerating

After changing anything in `config/` or the API types:

```sh
kubebuilder edit --plugins=helm/v2-alpha --force
```

The plugin first runs `make build-installer` (which runs `make manifests` and `make generate`),
then rebuilds `dist/chart` from the new `dist/install.yaml`. Commit `config/` and `dist/` together.

**NOTE:** Use kubebuilder v4.16.0 or later. v4.15 generates a manager
template that fails to render when the Deployment has its own volumes, such as
the `/tmp` mount in `config/manager/manager.yaml`.

**NOTE:** Always pass `--force`. Without it the plugin keeps the existing
`dist/chart/values.yaml`, so values that come from `config/manager` (volumes,
args) go stale while the templates are regenerated.

**NOTE:** `dist/chart/Chart.yaml` is the one exception. The plugin creates it
once and never overwrites it, even with `--force`. Bump its `version` and
`appVersion` by hand when releasing; the release workflow refuses a tag that
does not match `appVersion`.

**NOTE:** `make build-installer` and `make deploy` both run
`kustomize edit set image controller=${IMG}`, which rewrites the `images:`
entry in `config/manager/kustomization.yaml`. Expect that file in your diff.

### Installing with kustomize

```sh
make deploy IMG=<registry>/llm-operator:<tag>
```

`make deploy` builds `config/default` and applies it. See
[To Deploy on the cluster](#to-deploy-on-the-cluster).

### Installing with Helm

Install from the checkout, or with `make helm-deploy IMG=<registry>/llm-operator:<tag>`:

```sh
helm install llmscaleoperator ./dist/chart --namespace <ns> --create-namespace
```

The release workflow packages and publishes `dist/chart`.

**NOTE:** Run `make help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## File-based tests

Unit and envtest cases live in YAML under a `testdata/` directory next to the package, one file per case. The Go tests load those files, stand up fixtures, and check the result. Kind-based end-to-end tests under `test/e2e` are unchanged.

```
internal/<pkg>/testdata/<suite>/<case-name>.yaml
```

The suite directory groups a table (`guards/hold`, `controller/metric-scaling`). The file name is the case name in kebab-case. A document has:

- exactly one YAML mapping. An empty file, a comment-only file, `null`, a bare `---`, or a second document fails the load. A trailing `---` counts as a second document.
- `name`, optional. When set it must equal the file name without `.yaml`.
- `description`, the sentence `go test -v` and Ginkgo print. When it is empty, the file name is used.
- `inputs` and `expected`, decoded into a Go struct. An unknown field, a duplicate key, a key in the wrong case, or a value of the wrong type fails the load. Mapping keys must match the json name of the field, including case.

Only `*.yaml` is loaded. A `*.yml` file fails the load, so two extensions cannot share a stem. Other names are ignored. The suffix check is case-sensitive.

Durations are Go duration strings (`30s`, `1m1s`, `-2h`, `0s`). Only a pointer to a duration may be omitted or `null`. A non-pointer duration rejects `null` and treats an omission as zero. A pointer to zero is `"0s"`, not a bare `0`. Times relative to a fixed origin are offsets from that origin; the runner adds them.

The loader is `internal/testutil/cases`. It reads every `*.yaml` under the directory, in path order, and rejects an empty directory. `cases.Run` starts one subtest per file. Ginkgo tables use `ginkgocases.Entries` in `internal/testutil/cases/ginkgocases`, so a test binary that does not import that package does not register Ginkgo flags.

To add a case, copy a nearby file, change the inputs and the expected result, and run `make test`.

## License

Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

