# End-to-end tests for lite-auth-proxy

These tests check that the proxy behaves correctly **from the outside**, exactly
like a real client would see it — running the actual Docker image (or a deployed
service) and making real HTTP requests.

The tests are written in plain English using [Gherkin](https://cucumber.io/docs/gherkin/)
(the "Given / When / Then" style popularised by Cucumber). You do **not** need to
be a programmer to read them — or to add new ones. Open any file in
[`features/`](features) and you'll see scenarios like:

```gherkin
Scenario: A request with a valid API key is allowed through
  Given the proxy is running
  When I send a request to "/" with a valid API key
  Then the response status should be 200
  And the upstream should have received header "X-AUTH-SERVICE" equal to "internal"
```

> **AI note:** the framework is structured so an AI "translate English → test"
> layer can be added later. For now the scenarios use a fixed, deterministic
> vocabulary (the steps in [`features/steps`](features/steps)) — important for a
> *security* test suite, where pass/fail must be reliable, not guessed.

## What is covered

| Feature file | What it proves | Needs |
| --- | --- | --- |
| `health.feature` | The `/healthz` endpoint is open and returns OK | nothing |
| `jwt_auth.feature` | Valid Firebase logins pass; bad/missing tokens are rejected | a Firebase token |
| `apikey_auth.feature` | A valid API key is accepted; a wrong one is rejected | flex build |
| `rate_limiting.feature` | A flood of requests is turned away with HTTP 429 | local only |
| `admin.feature` | The admin control plane refuses unauthenticated calls | flex build |

Scenarios automatically **skip themselves** when their prerequisites aren't met
(e.g. API-key scenarios are skipped against a lite image), so the same files work
everywhere.

## Prerequisites

You need three things installed:

| Tool | Why | Install |
| --- | --- | --- |
| [Docker](https://www.docker.com/) | runs the proxy image being tested | Docker Desktop, or `brew install --cask docker` |
| [`uv`](https://docs.astral.sh/uv/) | creates the Python environment and runs the tests | `curl -LsSf https://astral.sh/uv/install.sh \| sh` (or `brew install uv`) |
| [`gcloud`](https://cloud.google.com/sdk/docs/install) | fetches the Firebase test login (only needed for the JWT scenarios) | Google Cloud SDK |

You do **not** need to install Python yourself — `uv` downloads and manages an
isolated Python for this directory. (`uv` and `gcloud` don't have to be on your
`PATH`; `run.sh` also looks in `~/.local/bin` and the default Cloud SDK location.)

## Setting up the Python environment

There are two ways — pick one.

**Option A — let `run.sh` do it (recommended).** You don't have to set anything
up by hand: `run.sh` calls `uv run`, which automatically creates a local
`.venv/` in this directory (from `pyproject.toml` + `uv.lock`) and installs
`behave` and `requests` the first time you run it. Just go to *Running the
tests* below.

**Option B — create the environment explicitly.** Handy if you want to run
`behave` directly or have your editor pick up the interpreter:

```bash
cd e2e
uv sync                       # creates ./.venv and installs the dependencies
source .venv/bin/activate     # optional: activate it in your shell
```

After `uv sync`, the test runner lives at `./.venv/bin/behave`, and you can run
behave through uv without activating anything: `uv run behave ...`.

## Running the tests

From this `e2e/` directory:

```bash
# Test the local image (default: flex). Reuses the image you've already built:
./run.sh local flex
./run.sh local lite

# (Re)build the image first, then test:
E2E_BUILD_IMAGE=1 ./run.sh local flex

# Test a service that is already deployed:
./run.sh remote https://your-proxy-url
```

(Or from the repo root: `make e2e-flex`, `make e2e-lite`,
`make e2e-remote URL=https://your-proxy-url`. The local `make` targets also
reuse the existing image — build it once with `make docker-build-flex` /
`make docker-build-lite`, or run with `E2E_BUILD_IMAGE=1`.)

`run.sh` starts a small stack (the proxy + a request-echoing upstream + a tiny
dedicated rate-limit proxy), waits until it's healthy, runs the scenarios, and
tears everything down again. By default it **reuses your existing local image**;
build it first with `make docker-build-flex` / `make docker-build-lite`, or pass
`E2E_BUILD_IMAGE=1` to build it as part of the run.

Run a subset by passing normal behave arguments through:

```bash
./run.sh local flex --tags=@smoke          # just the smoke test
./run.sh local flex features/jwt_auth.feature
```

## How JWT login works

The proxy validates JWTs against Google's live servers, so the tests sign in as a
real, dedicated test user in the `fp8devel` Firebase project and use the returned
ID token. The login (`<email>:<password>`) and the Firebase Web API key are read
at run time from Google Secret Manager via your `gcloud` login — nothing secret,
and no username, is stored here. To use a different test user, just update the
`LOGIN_FIREBASE_AUTH_DEV` secret.

If `gcloud` isn't available (e.g. in CI), supply a token directly and the
sign-in step is skipped:

```bash
E2E_JWT_TOKEN="$(your-token-command)" ./run.sh local flex
```

If no token can be obtained, the JWT scenarios are skipped (not failed).

## Adding a new scenario

1. Open the most relevant `features/*.feature` file.
2. Copy an existing scenario and adjust the English.
3. Re-run. If you used a phrase that doesn't exist yet, behave will print the
   missing step — add it to [`features/steps/proxy_steps.py`](features/steps/proxy_steps.py)
   (or ask a developer to).

## Layout

```
e2e/
  features/            # plain-English scenarios (*.feature) + step definitions
    environment.py     #   run setup: load settings, get a token, auto-skip rules
    steps/             #   the reusable Given/When/Then vocabulary
  proxylib/            # helpers: settings, Firebase token acquisition
  compose/             # the docker-compose stack used by the local profile
  run.sh               # the one command you run
```
