# Typst Renderer Daemon (`renderer`)

Der Renderer-Dienst (`cmd/renderer`, `internal/renderer`) führt die Typst-PDF-Kompilierung in einem eigenen, isolierten Container ohne Netzwerkzugriff (Egress) aus.

---

## 🎯 Zweck & Sicherheitsarchitektur

Typst lädt nicht-vendorte Pakete standardmäßig aus dem Netzwerk nach, und Template-Inhalte (insbesondere Markdown via `cmarker`) bieten potenzielle Angriffsflächen für Code-Injection. 

Um das Kernsystem und sensible Daten zu schützen:
1. **Kein Egress (Netzwerk-Isolation):** Der Renderer-Container läuft ausschließlich an einem Docker-Netzwerk mit `internal: true`. Ausgehende Verbindungen ins Internet sind unterbunden.
2. **Keine Secrets & kein Datenbankzugriff:** Der Dienst hat keinen Zugriff auf PostgreSQL, FileStore-Credentials oder API-Schlüssel. Er verarbeitet Vorlagen-Bundles und JSON-Daten rein im flüchtigen Speicher (`tmpfs`).
3. **Minimale Rechte:** Der Container läuft als unprivilegierter Nutzer (`nonroot`, UID 65532) mit `read_only: true` Root-Dateisystem.
4. **Ressourcen- und Parallelitätslimits:** Verhindert Denial-of-Service durch speicher- oder rechenintensive Kompilierungen.

---

## 🔌 gRPC-Schnittstelle

Definiert in `internal/renderer/proto/renderer/v1/renderer.proto`.

### Methoden

- **`Render(RenderRequest) returns (RenderResponse)`**
  Kompiliert das übergebene Bundle (`entrypoint`, Quelldateien als `files`-Map und `data_json`) zu einem PDF. Optional erzwingt `pdf_standard` einen PDF-Standard (z. B. `a-2b` für PDF/A-2b, mehrere kommagetrennt); erlaubt sind nur die Werte von `typst compile --pdf-standard`.
- **`Query(QueryRequest) returns (QueryResponse)`**
  Führt eine Typst-Query (z. B. zur Metadaten-Extraktion in Golden-File-Tests) auf dem Bundle aus und liefert die Treffer als JSON.

### Status-Codes

Jeder Fehler-Status trägt zusätzlich ein `google.rpc.ErrorInfo` (Domain `renderer.domio`) mit einem `ErrorReason` aus dem Proto, damit Clients Fehler mit gleichem gRPC-Code unterscheiden können (Helfer: `rendererv1.NewStatusError`, `rendererv1.ReasonOf`).

| Code | `ErrorReason` | Ursache | API (`GRPCRenderer`) |
| :--- | :--- | :--- | :--- |
| `OK` | – | Erfolgreich gerendert | – |
| `InvalidArgument` | `INVALID_BUNDLE` | Ungültiges Bundle (fehlender Entrypoint, Pfad außerhalb des Roots, unbekannter PDF-Standard, leerer Selector) | `ErrInvalidBundle` |
| `InvalidArgument` | `COMPILE_FAILED` | Typst-Kompilierfehler; die Nachricht enthält die Diagnose (auch: nicht vendortes Paket, Download schlägt fehl) | `ErrRenderFailed` + `typst.ErrCompileFailed` |
| `DeadlineExceeded` | `TIMEOUT` | Timeout = min(gRPC-Deadline, `RENDERER_MAX_TIMEOUT_SECONDS`) | `ErrRenderFailed` + `typst.ErrCompileTimeout` |
| `ResourceExhausted` | `REQUEST_TOO_LARGE` | Request größer als `RENDERER_MAX_REQUEST_BYTES` | `ErrRenderFailed` |
| `ResourceExhausted` | `OUTPUT_TOO_LARGE` | Ausgabe größer als das Typst-Limit (64 MiB) | `ErrRenderFailed` + `typst.ErrOutputTooLarge` |
| `ResourceExhausted` | `CONCURRENCY_LIMIT` | Alle `RENDERER_MAX_CONCURRENCY` Slots belegt (kein Warten, sofortige Ablehnung) | `ErrRendererUnavailable` (HTTP `503`, River-Retry) |
| `Canceled` | – | Der Aufrufer hat den Request abgebrochen | `ErrRenderFailed` |
| `Unavailable` | – | Dienst nicht erreichbar | `ErrRendererUnavailable` (HTTP `503`) |

Die API bildet die Fehler damit auf dieselben Domain-Fehler und Typst-Ursachen ab wie der lokale Adapter.

Die gRPC-Nachrichtengrößen sind an die Limits gekoppelt (`renderer.GRPCServerOptions`): Der Server nimmt Requests bis `RENDERER_MAX_REQUEST_BYTES` + 1 MiB Protobuf-Overhead an und sendet Antworten bis 64 MiB + 1 MiB; der API-Client akzeptiert Antworten in derselben Größe. Ohne diese Optionen gilt gRPCs Default von 4 MiB.

### Logging & Tracing

Typst-Diagnosen können Template-Quelltext und Daten (personenbezogen) zitieren. Sie stehen deshalb nur in der Status-Nachricht an den Aufrufer und im Error-Event des Spans (wie beim lokalen Adapter), nicht im Log und nicht im Span-Status. Logs enthalten Methode, Code, `ErrorReason`, Request-/Response-Größe und Dauer.

> [!NOTE]
> Traces und Metriken des Renderers erreichen derzeit keinen Collector: Das interne Netz hat keinen Egress und es ist noch kein `MeterProvider` konfiguriert. Siehe Backlog „Telemetrie des Renderer-Dienstes: interner Telemetrie-Proxy“.

---

## ⚙️ Konfiguration

| Umgebungsvariable | Beschreibung | Standardwert |
| :--- | :--- | :--- |
| `RENDERER_LISTEN_ADDR` | Listen-Adresse für den gRPC-Server | `:9100` |
| `RENDERER_MAX_REQUEST_BYTES` | Maximale Größe eines eingehenden Requests in Bytes | `20971520` (20 MiB) |
| `RENDERER_MAX_CONCURRENCY` | Maximale Anzahl gleichzeitiger Render-Vorgänge | Anzahl CPU-Kerne (`runtime.NumCPU()`) |
| `RENDERER_MAX_TIMEOUT_SECONDS` | Maximales Zeitlimit je Kompilierung | `20` Sekunden |
| `TYPST_BINARY_PATH` | Pfad zum Typst-Binary | `typst` (im Container `/bin/typst`) |
| `DOCUMENT_PACKAGE_PATH` | Pfad zu vendorten Typst-Paketen | `/packages` (im Container) bzw. `internal/document/packages` |
| `DOCUMENT_FONT_PATH` | Pfad zu optionalen Schriften | `/fonts` |

---

## 🐳 Docker & Devcontainer

Das Multi-Stage-Dockerfile befindet sich unter `build/renderer/Dockerfile`.

### Devcontainer

Der Service `renderer` ist in `.devcontainer/docker-compose.yml` definiert und hängt nur am Netz `renderer_internal` (`internal: true`). Die API läuft im Devcontainer-Service selbst, der **zusätzlich** an `renderer_internal` hängt und am Default-Netz bleibt (Egress für Go-Module, pnpm, JWKS).

> [!IMPORTANT]
> Die Netz- und Service-Änderung wirkt erst nach einmaligem **„Dev Containers: Rebuild Container“**. Vorher ist `renderer:9100` nicht auflösbar, und die Dokumenterzeugung liefert `503`.

```bash
docker compose up -d renderer   # Renderer (neu) bauen und starten
```

Die API nutzt standardmäßig `DOCUMENT_RENDERER=grpc` mit `DOCUMENT_RENDERER_ADDR=renderer:9100`. Für die lokale Entwicklung ohne Renderer-Container (z. B. vor dem Rebuild) `DOCUMENT_RENDERER=local` in `.env` setzen; dann kompiliert die API selbst mit dem Typst aus dem Devcontainer (Warn-Log beim Start).

---

## ⬆️ Typst-Upgrade

Die Typst-Version steht ausschließlich in `build/typst.version`. Für ein Upgrade:

1. `build/typst.version` auf die neue Version setzen.
2. Die SHA-256-Prüfsummen der Release-Archive (`typst-x86_64-unknown-linux-musl.tar.xz`, `typst-aarch64-unknown-linux-musl.tar.xz`) von der GitHub-Release-Seite übernehmen — in `build/renderer/Dockerfile` **und** `.devcontainer/setup.sh` (beide prüfen die Checksumme und brechen bei Abweichung ab).
3. Devcontainer neu bauen bzw. `.devcontainer/setup.sh` ausführen, damit lokal dieselbe Version läuft (der Golden-Helper bricht bei abweichender Version ab).
4. Changelog auf Breaking Changes prüfen, v. a. für die vendorten Pakete unter `internal/document/packages` (z. B. `cmarker`) und die eingebauten Schriften.
5. Golden-Dateien neu erzeugen und die Diffs sichten: `go test ./internal/document/... -update`.
6. Tests: `go test ./internal/infrastructure/typst/... ./internal/renderer/... ./internal/document/...`.
7. Renderer-Image neu bauen und deployen (`docker compose up -d --build renderer`).
