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

## API Auth And Org Scope Flow

```mermaid
flowchart TD
    client[ClientRequest] --> ginRouter[GinRouter]
    ginRouter --> reqId[RequestIDMiddleware]
    reqId --> reqLog[ZeroLogRequestLogger]
    reqLog --> recover[RecoveryMiddleware]
    recover --> cors[CorsMiddleware]
    cors --> healthRoute[HealthRoute]
    cors --> authRoute[AuthPublicRouteGroup]
    cors --> apiV1[ApiV1ProtectedRouteGroup]
    apiV1 --> jwtAuth[JwtMiddleware]
    jwtAuth --> jwks[SupabaseAuthKeys]
    jwtAuth --> usersTable[SupabaseUsersTableLookup]
    usersTable --> ctxScope[ContextUserAndOrg]
    ctxScope --> handler[OrgScopedHandler]
```

## GitHub App Integration Flow

```mermaid
flowchart TD
    githubWebhook[GitHubWebhook] --> webhookEndpoint[AuthWebhookEndpoint]
    webhookEndpoint --> sigValidation[SignatureValidation]
    sigValidation -->|valid| ackNow[Immediate200Ack]
    sigValidation -->|invalid| reject401[Reject401AndLog]
    ackNow --> asyncProcessing[AsyncWebhookProcessing]
    asyncProcessing --> eventTypeCheck[EventTypeFilter]
    eventTypeCheck --> installationLookup[InstallationToOrgLookup]
    installationLookup --> mappingTable[GitHubInstallationsTable]
    installationLookup --> githubApiLookup[GitHubInstallationLookup]
    githubApiLookup --> orgsLookup[OrgsTableBySlug]
    orgsLookup --> mappingUpsert[GitHubInstallationsUpsert]
    mappingTable --> eventsInsert[EventsTableInsert]
    mappingUpsert --> eventsInsert

    oauthRedirect[GitHubOAuthRedirect] --> oauthCallback[AuthGitHubCallbackEndpoint]
    oauthCallback --> oauthCodeExchange[GitHubCodeExchange]
    oauthCodeExchange --> githubUserFetch[GitHubUserFetch]
    githubUserFetch --> supabaseSession[SupabaseSessionIssue]
    supabaseSession --> orgEnsure[OrgEnsureByGitHubLogin]
    orgEnsure --> usersUpsert[UsersTableUpsert]
    usersUpsert --> frontendTokens[FrontendSessionTokens]
```
