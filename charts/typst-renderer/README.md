# typst-renderer Helm chart

Deploys the [typst-renderer](../../README.md) gRPC daemon. Published with every release tag as an OCI
artifact next to the image:

```bash
helm install renderer oci://ghcr.io/mache-vermietung/charts/typst-renderer --version 1.0.2
```

The chart version is the release tag without the leading `v` (tag `v1.0.2` → chart `1.0.2`); the
image tag defaults to the chart's `appVersion` (`v1.0.2`), so chart and image always come from the
same commit. Values are validated against `values.schema.json`: unknown or misplaced keys fail the
render instead of being ignored.

## What it deploys

- `Deployment` hardened like the image expects: non-root (UID 65532), read-only root file system,
  all capabilities dropped, `RuntimeDefault` seccomp; `/tmp` as an in-memory `emptyDir`
  (`tmpSizeLimit`, counts against the memory limit). No service account token is mounted.
- Native gRPC probes against `grpc.health.v1`: startup and liveness on the whole server, readiness
  on `renderer.v1.RendererService` (reports `NOT_SERVING` while shutting down).
- `Service` (`ClusterIP`, port `grpc` 9100, `appProtocol: kubernetes.io/h2c`).
- Optional: `NetworkPolicy`, `PodDisruptionBudget` (always with `replicaCount > 1`).

There is no Prometheus endpoint: traces and metrics are exported over OTLP/HTTP when
`otel.endpoint` is set.

## Network isolation

The daemon is designed to run **without egress** (see "Security model" in the repository README).
With `networkPolicy.enabled: true` the chart creates a policy that

- allows ingress to the gRPC port only from `networkPolicy.ingress.from` (a list of
  `NetworkPolicyPeer`s) -- **an empty list allows no ingress at all**;
- allows egress only to the cluster DNS (`networkPolicy.dns`, default `kube-system` /
  `k8s-app: kube-dns`) and to the rules in `networkPolicy.egress.extra`, e.g. an OTLP collector.

```yaml
networkPolicy:
  enabled: true
  ingress:
    from:
      - namespaceSelector:
          matchLabels:
            renderer-client: "true"
  egress:
    extra:
      - to:
          - namespaceSelector:
              matchLabels:
                kubernetes.io/metadata.name: monitoring
            podSelector:
              matchLabels:
                app.kubernetes.io/name: alloy
        ports:
          - port: 4318
            protocol: TCP
otel:
  endpoint: http://alloy.monitoring:4318
```

## Values

| Key | Default | Description |
| :--- | :--- | :--- |
| `replicaCount` | `1` | Number of pods |
| `image.repository` | `ghcr.io/mache-vermietung/typst-renderer` | Image; derived images (vendored packages, fonts) go here |
| `image.tag` | `""` | Empty = `.Chart.AppVersion` |
| `image.pullPolicy` | `IfNotPresent` | |
| `imagePullSecrets` | `[]` | |
| `nameOverride`, `fullnameOverride` | `""` | |
| `service.type` | `ClusterIP` | |
| `service.port` | `9100` | Service port (the container always listens on 9100) |
| `config.maxConcurrency` | `2` | `RENDERER_MAX_CONCURRENCY`; `null` = number of CPUs the daemon sees (the node's, not the pod's) |
| `config.maxRequestBytes` | `20971520` | `RENDERER_MAX_REQUEST_BYTES` |
| `config.maxTimeoutSeconds` | `20` | `RENDERER_MAX_TIMEOUT_SECONDS` |
| `config.logLevel` | `info` | `debug`/`info`/`warn`/`error` |
| `config.logFormat` | `json` | `json` or `text` |
| `otel.endpoint` | `""` | `OTEL_EXPORTER_OTLP_ENDPOINT` (OTLP/HTTP); empty = no export |
| `otel.serviceName` | `""` | `OTEL_SERVICE_NAME`; empty = release full name |
| `env` | `{}` | Additional plain environment variables |
| `extraEnv` | `[]` | Additional `EnvVar` objects |
| `extraVolumes`, `extraVolumeMounts` | `[]` | E.g. packages or fonts mounted at runtime |
| `tmpSizeLimit` | `256Mi` | Size of the in-memory `/tmp` |
| `probes.readinessService` | `renderer.v1.RendererService` | Health service checked by the readiness probe |
| `resources` | requests `100m`/`512Mi`, limit `512Mi` | No CPU limit; memory request = limit |
| `podSecurityContext`, `securityContext` | `{}` | Merged over the hardened defaults |
| `serviceAccount.create` / `.name` / `.annotations` | `true` / `""` / `{}` | |
| `podDisruptionBudget.enabled` / `.maxUnavailable` | `false` / `1` | |
| `networkPolicy.*` | disabled | See above |
| `terminationGracePeriodSeconds` | `30` | |
| `priorityClassName`, `nodeSelector`, `affinity`, `tolerations`, `topologySpreadConstraints` | empty | Passed through |
| `podAnnotations`, `podLabels` | `{}` | |

## Development

```bash
helm lint charts/typst-renderer -f charts/typst-renderer/ci/all-on-values.yaml
helm template t charts/typst-renderer -f charts/typst-renderer/ci/all-on-values.yaml \
  | kubeconform -strict -summary
```

CI runs both plus an install test in kind (`.github/workflows/ci.yml`, job `chart`).
