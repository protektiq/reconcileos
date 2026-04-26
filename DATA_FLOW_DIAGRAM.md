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

## Web Frontend Auth And Route Guard Flow

```mermaid
flowchart TD
    webClient[WebClientBrowser] --> loginRoute[LoginRoute]
    loginRoute --> githubOAuth[SupabaseGitHubOAuth]
    githubOAuth --> callbackRoute[AuthCallbackRoute]
    callbackRoute --> sessionSet[ZustandAuthStoreSetSession]
    sessionSet --> protectedRoutes[ProtectedRoutes]
    protectedRoutes --> dashboardRoute[DashboardRoute]
    protectedRoutes --> marketplaceRoute[MarketplaceRoute]
    protectedRoutes --> queueRoute[QueueRoute]
    protectedRoutes --> attestationsRoute[AttestationsRoute]
    protectedRoutes -->|missingSession| loginRoute
```

## Runtime Event Dispatch And Execution Flow

```mermaid
flowchart TD
    githubWebhookEvent[GitHubWebhookEvent] --> eventsTable[SupabaseEventsTable]
    runtimeMain[RuntimeMain] --> dispatcherLoop[DispatcherLoop5s]
    runtimeMain --> executorLoop[ExecutorLoop]

    dispatcherLoop --> dispatchLock[RuntimeTryLockDispatch]
    dispatchLock --> unprocessedEvents[QueryProcessedFalseLimit50]
    unprocessedEvents --> activeInstalls[ActiveBotInstallationsByOrg]
    activeInstalls --> botManifestLoad[LoadBotManifestJson]
    botManifestLoad --> triggerMatch[MatchEventTypeToTriggers]
    triggerMatch --> queuedExecutionInsert[InsertExecutionQueued]
    queuedExecutionInsert --> markProcessed[MarkEventProcessedTrue]

    executorLoop --> execLock[RuntimeTryLockExecutor]
    execLock --> queuedExecutions[QueryQueuedExecutionsLimit10]
    queuedExecutions --> markRunning[SetExecutionRunningStartedAt]
    markRunning --> runtimeSubprocess[SpawnBotSubprocess]
    runtimeSubprocess --> executionWorkdir[TmpExecutionWorkspace]
    runtimeSubprocess --> outputCapture[CaptureStdoutStderr]
    outputCapture --> statusUpdate[UpdateExecutionCompletedOrFailed]
    statusUpdate --> reviewFlag[SetRequiresReviewWhenRequested]

    runtimeMain --> gracefulShutdown[SigtermDrain30s]
    gracefulShutdown --> forceCancel[CancelRemainingProcessesAfterDrain]
```

## OpenRewrite CVE Patcher Flow

```mermaid
flowchart TD
    runtimeExecution[RuntimeExecutionStart] --> botContainer[OpenRewriteCvePatcherContainer]
    botContainer --> envValidation[ValidateRequiredExecutionEnv]
    envValidation --> tokenRequest[CallApiV1GithubInstallationToken]
    tokenRequest --> orgScopedLookup[ApiOrgScopedInstallationLookup]
    orgScopedLookup --> githubMint[GitHubAppInstallationTokenMint]
    githubMint --> cloneRepo[CloneTargetRepoWithAskPass]
    cloneRepo --> runRecipes[RunOpenRewriteSecurityRecipes]
    runRecipes --> gitDiff[CollectUnifiedDiffAndChangedFiles]
    gitDiff --> jsonOutput[EmitExecutionResultJson]
    botContainer --> cleanupTemp[DeleteTempCloneDirectoryOnExit]

    subgraph trustBoundary [TrustBoundaryBotContainer]
      envValidation
      tokenRequest
      cloneRepo
      runRecipes
      gitDiff
      cleanupTemp
    end
```

### Credential Lifecycle Notes
- The bot requests a short-lived GitHub installation token from a JWT-protected API endpoint scoped to the authenticated org.
- The token remains in process memory only and is supplied to git through `GIT_ASKPASS` to avoid git credential helper persistence.
- Temporary clone workspace and helper script are deleted on every exit path via trap-based cleanup.

## Sigstore Attestation Engine Flow

```mermaid
flowchart TD
    executionContext[ExecutionContextLookup] --> slsaBuild[SlsaStatementBuild]
    slsaBuild --> oidcIdentity[OidcIdentityResolve]
    oidcIdentity --> keylessSign[SigstoreKeylessSign]
    keylessSign --> rekorSubmit[RekorSubmitAndInclusion]
    rekorSubmit --> attestationWrite[AttestationsTableInsert]
    attestationWrite --> orgList[OrgScopedListAndGet]
    rekorSubmit --> publicVerify[PublicVerifyByArtifactHash]
    attestationWrite --> complianceExport[ComplianceBundleExport]
```
