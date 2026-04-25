# ReconcileOS Data Flow Diagram

```mermaid
flowchart LR
    web[WebFrontend] -->|RESTAPIrequests| api[GoGinAPI]
    cli[RustCLIrecos] -->|ControlAndOps| api
    api -->|RuntimeCommands| runtime[GoBotRuntime]
    runtime -->|LoadAndRun| bots[BotsArtifacts]
    api -->|PersistState| supabase[Supabase]
    infra[InfraConfig] -->|DeployAndSecrets| api
    infra -->|DeployAndSecrets| runtime
    infra -->|DeployAndSecrets| web
    api -->|SignedEventLogs| rekor[Rekor]
```

## Notes

- This diagram is intentionally high-level for bootstrap phase.
- Detailed trust boundaries and threat model annotations can be added once service interfaces are implemented.
