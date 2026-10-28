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
  Kompiliert das übergebene Bundle (`entrypoint`, Quelldateien als `files`-Map und `data_json`) zu einem PDF.
- **`Query(QueryRequest) returns (QueryResponse)`**
  Führt eine Typst-Query (z. B. zur Metadaten-Extraktion in Golden-File-Tests) auf dem Bundle aus und liefert die Treffer als JSON.

### Status-Codes

| Code | Ursache |
| :--- | :--- |
| `OK` | Erfolgreich gerendert |
| `InvalidArgument` | Ungültiges Bundle (z. B. fehlender Entrypoint, Pfadtraversierung) oder Typst-Kompilierfehler mit Fehlermeldung |
| `ResourceExhausted` | Maximale Request-Größe (`RENDERER_MAX_REQUEST_BYTES`) oder maximale Parallelität (`RENDERER_MAX_CONCURRENCY`) überschritten |
| `DeadlineExceeded` | Timeout während der Kompilierung (`RENDERER_MAX_TIMEOUT_SECONDS` oder gRPC-Deadline) |
| `Unavailable` | Dienst nicht erreichbar |

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

### Container starten (Devcontainer)
```bash
docker compose up -d renderer
```

> [!NOTE]
> Die API nutzt standardmäßig `DOCUMENT_RENDERER=grpc` mit `DOCUMENT_RENDERER_ADDR=renderer:9100`.
> Für die rein lokale Entwicklung ohne Container kann in `.env` `DOCUMENT_RENDERER=local` gesetzt werden.
