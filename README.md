# Spring Boot Threeport Module

A [Threeport](https://threeport.io) module that manages Java Spring Boot
application deployments.

Like every Threeport module, this is a Go project. Spring Boot is the workload
the module deploys, not the language it is written in: the controller
reconciles `SpringBootDefinition` and `SpringBootInstance` objects by generating
Kubernetes manifests and handing them to the Threeport API as a Kubernetes
workload.

## Objects

**`SpringBootDefinition`** describes how a Spring Boot application is built and
configured. It is the reusable part: one definition can back many instances.

| Field | Required | Notes |
|---|---|---|
| `Image` | yes | The application's container image. A Spring Boot project is packaged as a jar it builds itself, so there is no canonical public image and the module cannot deploy anything without one. |
| `Profile` | no | Passed as `SPRING_PROFILES_ACTIVE`. Spring reads it as a comma separated list; the module does not interpret it. |
| `ServerPort` | no | The port the image serves on. Defaults to `8080`, which is Spring Boot's default unless the project changed `server.port`. The Service targets whatever this says. |
| `JavaOpts` | no | Passed as `JAVA_TOOL_OPTIONS`, e.g. `-Xmx512m`. |
| `Database` | no | `none` or `postgres`. Defaults to `none`. |
| `HealthPath` | no | The path the probes ask for. Defaults to `/actuator/health`. |
| `Environment` | no | Drives replica and storage defaults. Defaults to `dev`. |
| `Replicas` | no | Overrides the replica count derived from `Environment`. |

**`SpringBootInstance`** is a running deployment of a definition.

| Field | Required | Notes |
|---|---|---|
| `SubDomain` | no | The subdomain used to reach this instance when a domain name is attached. Not yet acted on — see limitations. |
| `KubernetesRuntimeInstanceID` | no | The runtime to deploy to. Falls back to the control plane's default runtime when unset. |
| `SpringBootDefinitionID` | yes | The definition this instance deploys. |

## How it differs from the Django module

This module is modelled on [django-threeport-module](https://github.com/threeport/django-threeport-module),
and the differences are all consequences of the runtime rather than taste.

**The database is optional.** A Django project cannot start without one. A
Spring Boot application can run on an embedded database — petclinic defaults to
an in-memory H2 — so PostgreSQL is deployed only when `Database: postgres` is
asked for, and an instance that needs no database skips the credential work
entirely rather than waiting for a namespace it has no use for.

**There is no migration job.** Django requires `django-admin migrate` on any
schema change. Spring Boot applications bring their own schema handling, and
petclinic's postgres profile sets `spring.sql.init.mode=always`, so the schema
is created by the application on startup.

**The probes are HTTP, not TCP.** A JVM accepts connections as soon as the
listener binds, which is well before the application context has finished
refreshing, so a `tcpSocket` check reports an application ready while its beans
are still being wired. All three probes ask for `HealthPath` over HTTP, and a
`startupProbe` with a five minute budget sits in front of the other two:
without it the liveness probe restarts a cold JVM mid-boot and it never
finishes starting.

**There is no application secret.** Django refuses to load its settings without
a `SECRET_KEY`. Spring Boot has no mandatory equivalent, so the only secret is
the database credential, and only when a database is deployed.

## Status

| Piece | State |
|---|---|
| SDK config and API objects | done |
| Generated API server, client, controller scaffolding | done |
| Kubernetes manifests for the Spring Boot app | done |
| Definition reconciler | done |
| Instance reconciler | done |
| Config abstractions (`pkg/config`) | done — see `samples/` |
| tptctl plugin | generated, not yet exercised |
| Sample application | `examples/spring-petclinic` — builds and runs, see below |
| Verified against a live control plane | yes — kind, see below |

## What has been verified

The sample image was built and exercised directly, outside Threeport, to check
the two assumptions the module's design rests on.

**The datasource is configured from the environment.** Petclinic's `postgres`
profile sets `spring.datasource.url=${POSTGRES_URL:jdbc:postgresql://localhost/petclinic}`
in a properties file packaged inside the jar, and the module sets
`SPRING_DATASOURCE_URL` instead. Environment variables outrank config data in
Spring Boot's property precedence, so the module's value should win — and it
does: run against a PostgreSQL whose database and user are `springboot` rather
than petclinic's own defaults, the application logs
`Database JDBC URL [jdbc:postgresql://pc-pg:5432/springboot]` and connects.
This is why the module sets the generic `SPRING_DATASOURCE_*` names rather than
the `POSTGRES_*` ones petclinic happens to read: the generic ones work for any
Spring Boot application, and they win regardless.

**The application creates its own schema.** Its `postgres` profile sets
`spring.sql.init.mode=always`, and a run against an empty database produced all
seven tables and loaded the seed data. There is nothing for a migration job to
do, which is why the module has none.

**The health endpoint is real.** Petclinic includes
`spring-boot-starter-actuator`, so `/actuator/health` answers
`{"status":"UP"}` — and `/actuator/health/liveness` and `/readiness` answer too,
should a future version want to use them separately.

The image starts in about six seconds on a warm machine with no resource
limits, which is not what a cold pod under a CPU limit will do. The
`startupProbe` budget is five minutes for that reason.

### Against a live control plane

The module was installed into a Threeport control plane on kind and exercised
end to end, both with and without a database.

Without one, a defined instance deploys two resources — the application
deployment and its service — and petclinic runs on its embedded H2: the service
answers `{"status":"UP"}` on `/actuator/health`, serves the application, and
returns the seeded records. The rendered probes are the ones the module
intends: HTTP checks against `/actuator/health` on the container port, with the
startup probe's sixty attempts at five seconds in front of the other two.

With `Database: postgres`, the workload is six resources — the volume claim,
the PostgreSQL deployment and service, and the application deployment and
service. The Secret is not among them: the instance reconciler creates it in
the instance's namespace, which Threeport names while reconciling. The pods sit
in `CreateContainerConfigError` until it appears and then start unattended.
The application connects to `jdbc:postgresql://petclinic-postgres:5432/springboot`
— the module's service and database rather than petclinic's own defaults —
creates its schema, and serves the seeded records from it.

Deleting an instance removes the workload, its namespace and its volume, with
no orphaned PersistentVolume left behind; deleting a definition removes the
workload definition.

One defect came out of those runs and is described below.


## Known limitations

**Not yet run against a live control plane.** Everything above is covered by
unit tests, including the manifests and the propagation of config values to the
API, but no `SpringBootInstance` has been deployed to a real cluster yet. The
Django module turned up four defects that only appeared on a live run and none
of them failed a test, so treat this as untested until that section says
otherwise.

**An application that initialises its schema on startup cannot be deployed at
more than one replica against an empty database.** Petclinic does this rather
than using Flyway or Liquibase, and PostgreSQL's `CREATE INDEX IF NOT EXISTS`
checks the catalog and then creates, which is not atomic. Two replicas starting
together both pass the check, and the second dies with `duplicate key value
violates unique constraint "pg_class_relname_nsp_index"`. It recovers on
restart, because the schema exists by the time it comes back, so the deployment
does reach ready — but the first deploy is a crash loop, and `prod` defaults to
three replicas, so most of them crash.

The module cannot fix this: the schema initialisation happens inside the
application, and a Deployment creates all of its replicas at once. An
application using Flyway or Liquibase takes a lock and is safe at any replica
count. `samples/spring-boot-definition.yaml` therefore asks for one replica and
says why.

**No managed database.** The database is always a containerized Postgres
deployed alongside the application. The WordPress module offers a
`ManagedDatabase` flag that delegates to an `AwsRelationalDatabaseDefinition`,
but that object does not exist in the Threeport `0.7` line. The field was left
out rather than accepted and ignored.

**The credential is a plain Kubernetes Secret, not a Threeport `Secret`.** It
is generated by the instance reconciler and written into the instance's
namespace as `<definition>-db`. A Threeport `Secret` would pull in
`external-secrets` and a cloud secret manager as a live dependency, which is a
larger commitment than this module needs. The API does not expose the
credential, so the storage can be swapped later without an API change.

**There is no rotation path.** The reconciler creates the Secret if one is
absent and never rewrites an existing one: PostgreSQL only reads
`POSTGRES_PASSWORD` when it initialises an empty data directory, so a rewrite
would hand the application a password the running database does not accept.
Rotating it means deleting the Secret, the volume and the workload.

**A replace updates the API object but not the running workload.** Both update
reconcilers are unimplemented stubs, so a replace changes the stored definition
and reports success while the deployment goes on running what the previous
definition rendered. Changing a deployed application means deleting and
recreating it.

**`SubDomain` is stored but not acted on.** Reaching an instance by subdomain
needs a gateway and a domain name attached to it, which is a second set of
Threeport objects this module does not create yet. The field is modelled so the
API does not have to change when it is implemented.

## Threeport version

This module tracks the tip of the Threeport `0.7` branch rather than a release,
for the same reason the Django module does: the SDK that generates it emits
calls to `ProcessCoreTaggedFields*`, which do not exist in `v0.6.1`, the most
recent published release.

## Trying it out

`examples/spring-petclinic` builds
[spring-projects/spring-petclinic](https://github.com/spring-projects/spring-petclinic),
which Richard suggested as the sample application. It is a real Spring Boot
application rather than something written for this module: it includes the
actuator, so `/actuator/health` is a genuine readiness signal, and its
`postgres` profile reads the datasource from the environment, which is how the
module configures it.

The source is fetched at build time from a pinned commit rather than vendored.
Petclinic's `main` carries a SNAPSHOT version, so an unpinned build is a
different application every time.

### 1. Build and publish the sample image

The dev flow pulls from the local registry that `mage dev:localRegistryUp`
starts on port 5001, not from `ImageNamespace`.

```bash
docker build -t localhost:5001/spring-petclinic:v0.1.0 examples/spring-petclinic
docker push localhost:5001/spring-petclinic:v0.1.0
```

The build compiles petclinic from source with Maven, so the first run downloads
a full dependency tree and takes several minutes.

### 2. Build and install the module

```bash
mage build:allImagesDev     # api, database migrator, controller
mage install:plugin         # puts the tptctl plugin in ~/.threeport/plugins
tptctl spring-boot install -r localhost:5001
```

`-r localhost:5001` matters: the install defaults to `ImageNamespace`, and the
dev images are in the local registry.

### 3. Deploy the application

```bash
tptctl spring-boot create spring-boot -c samples/spring-boot.yaml
```

`samples/spring-boot.yaml` creates a definition and an instance that share a
name — a defined instance, running on petclinic's embedded H2.
`samples/spring-boot-definition.yaml` asks for PostgreSQL instead and shows
every field; `samples/spring-boot-instance.yaml` deploys another instance from
an existing definition.

A config file is read strictly: a field the values objects do not carry is an
error rather than something quietly ignored. `Image` is required, `Name` and
`Environment` have to be usable as Kubernetes label values, `Replicas` cannot
be negative, `ServerPort` has to be a real port, `HealthPath` has to start with
a slash, and `Database` has to be `none` or `postgres` — all of them reported
before anything is sent to the API.

That last one is the only check that is not also made downstream. The manifest
deploys PostgreSQL for `postgres` and an application with no database for
anything else, so a typo such as `postgresql` would otherwise deploy cleanly
and leave the application without the database it was configured to use.
