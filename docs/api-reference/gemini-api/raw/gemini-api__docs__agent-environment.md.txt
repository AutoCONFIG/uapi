# Environments in Managed Agents

Environments are managed Linux sandboxes that give agents an isolated place to
execute code and persist files. They are decoupled from interaction context, so
you can reuse the same environment across multiple interactions or start fresh
at any time.

The following example demonstrates how to create an interaction with a fresh
remote environment and retrieve its ID:

### Python

    from google import genai

    client = genai.Client()

    interaction = client.interactions.create(
        agent="antigravity-preview-05-2026",
        input="Install pandas and matplotlib, verify the imports, and print the versions.",
        environment="remote",
    )

    print(f"Environment ID: {interaction.environment_id}")

### JavaScript

    import { GoogleGenAI } from "@google/genai";

    const client = new GoogleGenAI({});

    const interaction = await client.interactions.create({
        agent: "antigravity-preview-05-2026",
        input: "Install pandas and matplotlib, verify the imports, and print the versions.",
        environment: "remote",
    });

    console.log(`Environment ID: ${interaction.environment_id}`);

### REST

    curl -X POST "https://generativelanguage.googleapis.com/v1beta/interactions" \
    -H "Content-Type: application/json" \
    -H "x-goog-api-key: $GEMINI_API_KEY" \
    -H "Api-Revision: 2026-05-20" \
    -d '{
        "agent": "antigravity-preview-05-2026",
        "input": "Install pandas and matplotlib, verify the imports, and print the versions.",
        "environment": "remote"
    }'

## The `environment` parameter

The `environment` parameter accepts three forms:

| Form | Example | When to use |
|---|---|---|
| `"remote"` | `environment="remote"` | Provision a fresh sandbox. |
| Environment ID | `environment="env_abc123"` | Reuse an existing sandbox with all its files and packages. |
| Config object | `environment={...}` | Provision a new sandbox with sources, network rules, or both. |

The following examples demonstrate the three ways of using the `environment`
parameter.

### Python

    from google import genai

    client = genai.Client()

    # Fresh sandbox
    interaction = client.interactions.create(
        agent="antigravity-preview-05-2026",
        input="Write a hello world script.",
        environment="remote",
    )

    # Reuse an existing sandbox
    interaction_2 = client.interactions.create(
        agent="antigravity-preview-05-2026",
        input="Modify the script to accept a name argument.",
        environment=interaction.environment_id,
        previous_interaction_id=interaction.id,
    )

    # New sandbox with sources
    interaction_3 = client.interactions.create(
        agent="antigravity-preview-05-2026",
        input="List all files and summarize the project.",
        environment={
            "type": "remote",
            "sources": [
                {
                    "type": "repository",
                    "source": "https://github.com/octocat/Spoon-Knife",
                    "target": "/workspace/spoon-knife",
                }
            ],
        },
    )

    print(interaction.output_text)

### JavaScript

    import { GoogleGenAI } from "@google/genai";

    const client = new GoogleGenAI({});

    // Fresh sandbox
    const interaction = await client.interactions.create({
        agent: "antigravity-preview-05-2026",
        input: "Write a hello world script.",
        environment: "remote",
    });

    // Reuse an existing sandbox
    const interaction2 = await client.interactions.create({
        agent: "antigravity-preview-05-2026",
        input: "Modify the script to accept a name argument.",
        environment: interaction.environment_id,
        previous_interaction_id: interaction.id,
    });

    // New sandbox with sources
    const interaction3 = await client.interactions.create({
        agent: "antigravity-preview-05-2026",
        input: "List all files and summarize the project.",
        environment: {
            type: "remote",
            sources: [
                {
                    type: "repository",
                    source: "https://github.com/octocat/Spoon-Knife",
                    target: "/workspace/spoon-knife",
                },
            ],
        },
    });

    console.log(interaction.output_text);

### REST

    # Fresh sandbox
    curl -X POST "https://generativelanguage.googleapis.com/v1beta/interactions" \
    -H "Content-Type: application/json" \
    -H "x-goog-api-key: $GEMINI_API_KEY" \
    -H "Api-Revision: 2026-05-20" \
    -d '{
        "agent": "antigravity-preview-05-2026",
        "input": [{"type": "text", "text": "Write a hello world script."}],
        "environment": "remote"
    }'

    # Reuse an existing sandbox (replace $ENV_ID and $INTERACTION_ID with values from the previous response)
    curl -X POST "https://generativelanguage.googleapis.com/v1beta/interactions" \
    -H "Content-Type: application/json" \
    -H "x-goog-api-key: $GEMINI_API_KEY" \
    -H "Api-Revision: 2026-05-20" \
    -d "{
        \"agent\": \"antigravity-preview-05-2026\",
        \"input\": [{\"type\": \"text\", \"text\": \"Modify the script to accept a name argument.\"}],
        \"environment\": \"$ENV_ID\",
        \"previous_interaction_id\": \"$INTERACTION_ID\"
    }"

    # New sandbox with sources
    curl -X POST "https://generativelanguage.googleapis.com/v1beta/interactions" \
    -H "Content-Type: application/json" \
    -H "x-goog-api-key: $GEMINI_API_KEY" \
    -H "Api-Revision: 2026-05-20" \
    -d '{
        "agent": "antigravity-preview-05-2026",
        "input": [{"type": "text", "text": "List all files and summarize the project."}],
        "environment": {
            "type": "remote",
            "sources": [
                {
                    "type": "repository",
                    "source": "https://github.com/octocat/Spoon-Knife",
                    "target": "/workspace/spoon-knife"
                }
            ]
        }
    }'

## Configure an environment

One way to set up an environment is to tell the agent what you need installed.
It handles dependency resolution and troubleshooting. Once the environment is
ready, save the `environment_id` and reuse it.

### Python

    from google import genai

    client = genai.Client()

    interaction = client.interactions.create(
        agent="antigravity-preview-05-2026",
        input="Install pandas, matplotlib, and seaborn. Verify all imports work and print the installed versions.",
        environment="remote",
    )

    # Reuse the configured environment
    interaction_2 = client.interactions.create(
        agent="antigravity-preview-05-2026",
        input="Clone https://github.com/octocat/Spoon-Knife into /workspace/tools. Run the test suite and fix any missing dependencies.",
        environment=interaction.environment_id,
        previous_interaction_id=interaction.id,
    )

    # Reuse the configured environment
    interaction_3 = client.interactions.create(
        agent="antigravity-preview-05-2026",
        input="Using the tools in /workspace/tools, list the files.",
        environment=interaction.environment_id,
        previous_interaction_id=interaction_2.id,
    )

    print(interaction.output_text)

### JavaScript

    import { GoogleGenAI } from "@google/genai";

    const client = new GoogleGenAI({});

    const interaction = await client.interactions.create({
        agent: "antigravity-preview-05-2026",
        input: "Install pandas, matplotlib, and seaborn. Verify all imports work and print the installed versions.",
        environment: "remote",
    });

    const interaction2 = await client.interactions.create({
        agent: "antigravity-preview-05-2026",
        input: "Clone https://github.com/octocat/Spoon-Knife into /workspace/tools. Run the test suite and fix any missing dependencies.",
        environment: interaction.environment_id,
        previous_interaction_id: interaction.id,
    });

    const interaction3 = await client.interactions.create({
        agent: "antigravity-preview-05-2026",
        input: "Using the tools in /workspace/tools, list the files.",
        environment: interaction.environment_id,
        previous_interaction_id: interaction2.id,
    });
    console.log(interaction.output_text);

### REST

    # Create interaction
    curl -X POST "https://generativelanguage.googleapis.com/v1beta/interactions" \
    -H "Content-Type: application/json" \
    -H "x-goog-api-key: $GEMINI_API_KEY" \
    -H "Api-Revision: 2026-05-20" \
    -d '{
        "agent": "antigravity-preview-05-2026",
        "input": "Install pandas, matplotlib, and seaborn. Verify all imports work and print the installed versions.",
        "environment": "remote"
    }'

### Mount from a source

If you know exactly what files the agent needs, mount them in a single call
instead of iterating. The `environment` config object accepts a `sources` array
with three types:

| Source type | `type` value | Description | Limit |
|---|---|---|---|
| Git repository | `repository` | Clones a repository from a URL into the sandbox at `target`. | 500 MB |
| Cloud Storage | `gcs` | Copies a file or directory from Cloud Storage into the sandbox at `target`. | 2 GB |
| Inline content | `inline` | Writes raw text content to a file in the sandbox at `target`. | 1 MB per file, 2 MB total |

### Python

    from google import genai

    client = genai.Client()

    interaction = client.interactions.create(
        agent="antigravity-preview-05-2026",
        input="List all files under /workspace and describe what you find.",
        environment={
            "type": "remote",
            "sources": [
                {
                    "type": "repository",
                    "source": "https://github.com/octocat/Spoon-Knife",
                    "target": "/workspace/spoon-knife",
                },
                {
                    "type": "gcs",
                    "source": "gs://cloud-samples-data/bigquery/us-states/",
                    "target": "/workspace/gcs-data",
                },
                {
                    "type": "inline",
                    "content": "# Project Notes\n\n- Analyze state population data\n- Create visualizations\n",
                    "target": "/workspace/notes/readme.md",
                },
            ],
        },
    )

    print(interaction.output_text)

### JavaScript

    import { GoogleGenAI } from "@google/genai";

    const client = new GoogleGenAI({});

    const interaction = await client.interactions.create({
        agent: "antigravity-preview-05-2026",
        input: "List all files under /workspace and describe what you find.",
        environment: {
            type: "remote",
            sources: [
                {
                    type: "repository",
                    source: "https://github.com/octocat/Spoon-Knife",
                    target: "/workspace/spoon-knife",
                },
                {
                    type: "gcs",
                    source: "gs://cloud-samples-data/bigquery/us-states/",
                    target: "/workspace/gcs-data",
                },
                {
                    type: "inline",
                    content: "# Project Notes\n\n- Analyze state population data\n- Create visualizations\n",
                    target: "/workspace/notes/readme.md",
                },
            ],
        },
    });

    console.log(interaction.output_text);

### REST

    # Create interaction with sources
    curl -X POST "https://generativelanguage.googleapis.com/v1beta/interactions" \
    -H "Content-Type: application/json" \
    -H "x-goog-api-key: $GEMINI_API_KEY" \
    -H "Api-Revision: 2026-05-20" \
    -d '{
        "agent": "antigravity-preview-05-2026",
        "input": "List all files under /workspace and describe what you find.",
        "environment": {
            "type": "remote",
            "sources": [
                {
                    "type": "repository",
                    "source": "https://github.com/octocat/Spoon-Knife",
                    "target": "/workspace/spoon-knife"
                },
                {
                    "type": "gcs",
                    "source": "gs://cloud-samples-data/bigquery/us-states/",
                    "target": "/workspace/gcs-data"
                },
                {
                    "type": "inline",
                    "content": "# Project Notes\n\n- Analyze state population data\n- Create visualizations\n",
                    "target": "/workspace/notes/readme.md"
                }
            ]
        }
    }'

You can combine both approaches: mount known sources declaratively, then iterate
with follow-up interactions to install packages or run setup scripts. You can't
set root (`/`) as target when adding a custom source, you must always specify a
sub-directory.

### Private sources

You can also download from private Github repositories or private Cloud
Storage buckets by adding the credentials in the network configuration:

For **private Git repositories** , use `Basic` authentication with your
[GitHub Personal Access Token
(PAT)](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens).
Encode the token using `x-oauth-basic` as the username:

    echo -n "x-oauth-basic:ghp_YourPATHere" | base64

### Python

    interaction = client.interactions.create(
        agent="antigravity-preview-05-2026",
        input="Run the test for my backend app and fix any issue.",
        environment={
            "type": "remote",
            "sources": [
                {
                    "type": "repository",
                    "source": "https://github.com/your-org/backend",
                    "target": "/backend-app"
                }
            ],
            "network": {
                "allowlist": [
                    {
                        "domain": "github.com",
                        "transform": {
                            "Authorization": "Basic YOUR_BASE64_TOKEN"
                        }
                    },
                    {
                        "domain": "*"
                    }
                ]
            }
        }
    )

### JavaScript

    const interaction = await client.interactions.create({
        agent: "antigravity-preview-05-2026",
        input: "Run the test for my backend app and fix any issue.",
        environment: {
            type: "remote",
            sources: [
                {
                    type: "repository",
                    source: "https://github.com/your-org/backend",
                    target: "/backend-app"
                }
            ],
            network: {
                allowlist: [
                    {
                        domain: "github.com",
                        transform: {
                            "Authorization": "Basic YOUR_BASE64_TOKEN"
                        }
                    },
                    {
                        domain: "*"
                    }
                ]
            }
        },
    });

### REST

    curl -X POST "https://generativelanguage.googleapis.com/v1beta/interactions" \
    -H "Content-Type: application/json" \
    -H "x-goog-api-key: $GEMINI_API_KEY" \
    -H "Api-Revision: 2026-05-20" \
    -d '{
        "agent": "antigravity-preview-05-2026",
        "input": "Run the test for my backend app and fix any issue.",
        "environment": {
            "type": "remote",
            "sources": [
                {
                    "type": "repository",
                    "source": "https://github.com/your-org/backend",
                    "target": "/backend-app"
                }
            ],
            "network": {
                "allowlist": [
                    {
                        "domain": "github.com",
                        "transform": {
                            "Authorization": "Basic YOUR_BASE64_TOKEN"
                        }
                    },
                    {
                        "domain": "*"
                    }
                ]
            }
        }
    }'

For **private Cloud Storage buckets**, use a standard OAuth 2.0 Bearer token:

    gcloud auth print-access-token

### Python

    interaction = client.interactions.create(
        agent="antigravity-preview-05-2026",
        input="Analyze the discrepancies across the data in workspace",
        environment={
            "type": "remote",
            "sources": [
                {
                    "type": "gcs",
                    "source": "gs://my-private-bucket/data",
                    "target": "/workspace",
                }
            ],
            "network": {
                "allowlist": [
                    {
                        "domain": "*.googleapis.com",
                        "transform": {
                            "Authorization": "Bearer YOUR_GCS_TOKEN"
                        }
                    },
                    {
                        "domain": "*"
                    }
                ]
            }
        },
    )

### JavaScript

    const interaction = await client.interactions.create({
        agent: "antigravity-preview-05-2026",
        input: "Analyze the discrepancies across the data in workspace",
        environment: {
            type: "remote",
            sources: [
                {
                    type: "gcs",
                    source: "gs://my-private-bucket/data",
                    target: "/workspace",
                }
            ],
            network: {
                allowlist: [
                    {
                        domain: "storage.googleapis.com",
                        transform: {
                            "Authorization": "Bearer YOUR_GCS_TOKEN"
                        }
                    },
                    {
                        domain: "*"
                    }
                ]
            }
        },
    });

### REST

    curl -X POST "https://generativelanguage.googleapis.com/v1beta/interactions" \
    -H "Content-Type: application/json" \
    -H "x-goog-api-key: $GEMINI_API_KEY" \
    -H "Api-Revision: 2026-05-20" \
    -d '{
        "agent": "antigravity-preview-05-2026",
        "input": "Analyze the discrepancies across the data in workspace",
        "environment": {
            "type": "remote",
            "sources": [
                {
                    "type": "gcs",
                    "source": "gs://my-private-bucket/data",
                    "target": "/workspace"
                }
            ],
            "network": {
                "allowlist": [
                    {
                        "domain": "storage.googleapis.com",
                        "transform": {
                            "Authorization": "Bearer YOUR_GCS_TOKEN"
                        }
                    },
                    {
                        "domain": "*"
                    }
                ]
            }
        }
    }'

## Pre-installed software

The sandbox runs on Ubuntu and comes with runtimes and common packages
pre-installed. The agent can install additional packages at runtime using `pip
install` or `npm install`. Packages installed during an interaction persist when
you reuse the same `environment_id`.

| Category | Pre-installed packages |
|---|---|
| **UNIX tools** | `curl`, `wget`, `git`, `rsync`, `unzip`, `ripgrep`, `fd-find`, `gawk`, `bc`, `tree`, `which`, `lsof`, `htop`, `jq`, `iproute2`, `procps`, `gcloud CLI` |
| **Python 3.12** | `numpy`, `pandas`, `requests`, `google-genai`, `beautifulsoup4`, `pyyaml`, `ast-grep-cli` |
| **Node.js 22** | `create-next-app`, `create-vite`, `typescript` |

## Network configuration

By default, environments have unrestricted outbound network access. Use the
`network` field to restrict outbound traffic to specific domains. Each rule
specifies a `domain` and an optional `transform` object to inject headers into
matching requests. These headers can be unique per interaction, and you can update them for the same environment.

| Field | Type | Description |
|---|---|---|
| `domain` | `string` | Domain to match. Use an exact hostname or `*` for all domains. |
| `transform` | `object` | Object containing flat key-value pairs representing headers to inject into matching requests, e.g. `{"Authorization": "Bearer ..."}`. |

### Python

    from google import genai

    client = genai.Client()

    interaction = client.interactions.create(
        agent="antigravity-preview-05-2026",
        input="Fetch the latest issues from the GitHub API for my-org/my-repo.",
        environment={
            "type": "remote",
            "network": {
                "allowlist": [
                    {
                        "domain": "api.github.com",
                        "transform": {
                            "Authorization": "Bearer ghp_your_github_token"
                        },
                    },
                    {"domain": "pypi.org"},
                    {"domain": "*"},
                ]
            },
        },
    )

    print(interaction.output_text)

### JavaScript

    import { GoogleGenAI } from "@google/genai";

    const client = new GoogleGenAI({});

    const interaction = await client.interactions.create({
        agent: "antigravity-preview-05-2026",
        input: "Fetch the latest issues from the GitHub API for my-org/my-repo.",
        environment: {
            type: "remote",
            network: {
                allowlist: [
                    {
                        domain: "api.github.com",
                        transform: {
                            "Authorization": "Bearer ghp_your_github_token"
                        },
                    },
                    { domain: "pypi.org" },
                    { domain: "*" },
                ]
            }
        },
    });

    console.log(interaction.output_text);

### REST

    curl -X POST "https://generativelanguage.googleapis.com/v1beta/interactions" \
    -H "Content-Type: application/json" \
    -H "x-goog-api-key: $GEMINI_API_KEY" \
    -H "Api-Revision: 2026-05-20" \
    -d '{
        "agent": "antigravity-preview-05-2026",
        "input": [{"type": "text", "text": "Fetch the latest issues from the GitHub API for my-org/my-repo."}],
        "environment": {
            "type": "remote",
            "network": {
                "allowlist": [
                    {
                        "domain": "api.github.com",
                        "transform": {
                            "Authorization": "Bearer ghp_your_github_token"
                        }
                    },
                    {"domain": "pypi.org"},
                    {"domain": "*"}
                ]
            }
        }
    }'

When an allowlist is set, only requests to explicitly listed domains are
permitted. You can use wildcards to match subdomains (e.g., `{"domain":
"*.example.com"}`), but note that this does not match the root domain
`example.com`, which must be added separately. To permit all other traffic, such
as routing unlisted domains without injected headers, add `{"domain": "*"}` as a
catch-all entry.

### Credentials

You can add credentials for your agent to use by adding header transformations. The credentials are
injected in the respective HTTP headers by an egress proxy, they are never
exposed inside the sandbox as environment variables or files.

### Python

    import subprocess
    from google import genai

    # Fetch a short-lived access token from your local gcloud CLI
    gcloud_token = subprocess.check_output(
        ["gcloud", "auth", "print-access-token"], text=True
    ).strip()

    client = genai.Client()

    interaction = client.interactions.create(
        agent="antigravity-preview-05-2026",
        input="List the files in gs://my-bucket/reports/ using the GCS JSON API.",
        environment={
            "type": "remote",
            "network": {
                "allowlist": [
                    {
                        "domain": "storage.googleapis.com",
                        "transform": {
                            "Authorization": f"Bearer {gcloud_token}"
                        },
                    }
                ]
            },
        },
    )

    print(interaction.output_text)

### JavaScript

    import { GoogleGenAI } from "@google/genai";

    import { execSync } from "child_process";

    const gcloudToken = execSync("gcloud auth print-access-token").toString().trim();

    const client = new GoogleGenAI({});

    const interaction = await client.interactions.create({
        agent: "antigravity-preview-05-2026",
        input: "List the files in gs://my-bucket/reports/ using the GCS JSON API.",
        environment: {
            type: "remote",
            network: {
                allowlist: [
                    {
                        domain: "storage.googleapis.com",
                        transform: {
                            "Authorization": `Bearer ${gcloudToken}`
                        },
                    }
                ]
            }
        },
    });

    console.log(interaction.output_text);

### REST

    curl -X POST "https://generativelanguage.googleapis.com/v1beta/interactions" \
    -H "Content-Type: application/json" \
    -H "x-goog-api-key: $GEMINI_API_KEY" \
    -H "Api-Revision: 2026-05-20" \
    -d '{
        "agent": "antigravity-preview-05-2026",
        "input": "List the files in gs://my-bucket/reports/ using the GCS JSON API.",
        "environment": {
            "type": "remote",
            "network": {
                "allowlist": [
                    {
                        "domain": "storage.googleapis.com",
                        "transform": {
                            "Authorization": "Bearer <YOUR_GCLOUD_TOKEN>"
                        }
                    }
                ]
            }
        }
    }'

### Disable network access

To block all outbound network access, set `network` to `disabled`:

### Python

    from google import genai

    client = genai.Client()

    interaction = client.interactions.create(
        agent="antigravity-preview-05-2026",
        input="Analyze the local files only.",
        environment={
            "type": "remote",
            "network": "disabled",
        },
    )

    print(interaction.output_text)

### JavaScript

    import { GoogleGenAI } from "@google/genai";

    const client = new GoogleGenAI({});

    const interaction = await client.interactions.create({
        agent: "antigravity-preview-05-2026",
        input: "Analyze the local files only.",
        environment: {
            type: "remote",
            network: "disabled",
        },
    });

    console.log(interaction.output_text);

### REST

    curl -X POST "https://generativelanguage.googleapis.com/v1beta/interactions" \
    -H "Content-Type: application/json" \
    -H "x-goog-api-key: $GEMINI_API_KEY" \
    -H "Api-Revision: 2026-05-20" \
    -d '{
        "agent": "antigravity-preview-05-2026",
        "input": "Analyze the local files only.",
        "environment": {
            "type": "remote",
            "network": "disabled"
        }
    }'

## Environment lifecycle

Environments follow this lifecycle:

| State | Behavior |
|---|---|
| **Created** | Provisioned when an interaction specifies `environment: "remote"` or a config object. |
| **Active** | Running while an interaction is in progress. |
| **Idle** | Auto-snapshot and stopped after 15 minutes of inactivity. |
| **Offline** | Retained for 7 days since last active. Can be resumed by passing its ID. |
| **Deleted** | Removed from the system. |

## Download files from the environment

The agent creates files inside the sandbox during execution. You can download the full environment snapshot as a tar file using the Files API:

### Python

    import os
    import requests
    import tarfile
    from google import genai

    client = genai.Client()

    interaction = client.interactions.create(
        agent="antigravity-preview-05-2026",
        input="Write a file environments_test.txt with content 'Environments' inside the sandbox.",
        environment="remote",
    )

    env_id = interaction.environment_id
    api_key = os.environ.get("GEMINI_API_KEY")

    response = requests.get(
        f"https://generativelanguage.googleapis.com/v1beta/files/environment-{env_id}:download",
        params={"alt": "media"},
        headers={"x-goog-api-key": api_key},
        allow_redirects=True,
    )

    with open("snapshot_env.tar", "wb") as f:
        f.write(response.content)

    os.makedirs("extracted_env_snapshot", exist_ok=True)
    with tarfile.open("snapshot_env.tar") as tar:
        tar.extractall(path="extracted_env_snapshot")

    print(os.listdir("extracted_env_snapshot"))

### JavaScript

    import { GoogleGenAI } from "@google/genai";
    import { execSync } from "child_process";
    import * as fs from "fs";

    const client = new GoogleGenAI({});

    const interaction = await client.interactions.create({
        agent: "antigravity-preview-05-2026",
        input: "Write a file environments_test.txt with content 'Environments' inside the sandbox.",
        environment: "remote",
    });

    const envId = interaction.environment_id;
    const apiKey = process.env.GEMINI_API_KEY || "";

    const url = `https://generativelanguage.googleapis.com/v1beta/files/environment-${envId}:download?alt=media`;
    const response = await fetch(url, {
        headers: {
            "x-goog-api-key": apiKey,
        },
    });

    if (!response.ok) {
        throw new Error(`Failed to download file: ${response.statusText}`);
    }

    const buffer = Buffer.from(await response.arrayBuffer());
    fs.writeFileSync("snapshot_env.tar", buffer);

    if (!fs.existsSync("extracted_env_snapshot")) {
        fs.mkdirSync("extracted_env_snapshot");
    }
    execSync("tar -xf snapshot_env.tar -C extracted_env_snapshot");

    console.log(fs.readdirSync("extracted_env_snapshot"));

### REST

    curl -X POST "https://generativelanguage.googleapis.com/v1beta/interactions" \
    -H "Content-Type: application/json" \
    -H "x-goog-api-key: $GEMINI_API_KEY" \
    -H "Api-Revision: 2026-05-20" \
    -d '{
        "agent": "antigravity-preview-05-2026",
        "input": "Write a file environments_test.txt with content '\''Environments'\'' inside the sandbox.",
        "environment": "remote"
    }'
    # Step 2: Download snapshot (reusing environment ID from Step 1)
    # curl -L -X GET "https://generativelanguage.googleapis.com/v1beta/files/environment-$ENV_ID:download?alt=media" \
    #   -H "x-goog-api-key: $API_KEY" \
    #   -o snapshot.tar

## Pricing \& resources

Each environment runs with fixed resource allocations:

| Resource | Value |
|---|---|
| **CPU** | 4 cores |
| **Memory** | 16 GB |

Environment compute (CPU, memory, sandbox execution) is **not billed** during
the preview period. See
[Pricing](https://ai.google.dev/gemini-api/docs/pricing#pricing-for-agents) for
agent token costs.

## Limitations

- **Preview status:** Environments and managed agents are in preview. Features and schemas may change.
- **Inline source size:** Inline sources are limited to 1mb per file, and 2mb total across all files.
- **Source size**: Git repositories are limited to 500 MB and Cloud Storage repositories to 2 GB.
- **Environment startup:** Provisioning a new environment takes up to \~5 seconds. Large source repositories may increase this time.
- **File support:** The agent is currently constrained to reading text and image files. Binary file support is not yet available.
- **No mounting from root:** You can't set root (`/`) as target when adding a custom source, you must always specify a sub-directory.

## What's next

- [Agents Overview](https://ai.google.dev/gemini-api/docs/agents): Learn about the core concepts of managed agents.
- [Quickstart](https://ai.google.dev/gemini-api/docs/managed-agents-quickstart): Start building with multi-turn conversations and streaming.
- [Antigravity Agent](https://ai.google.dev/gemini-api/docs/antigravity-agent): Explore capabilities, tools, and pricing for the default agent.
- [Building Custom Agents](https://ai.google.dev/gemini-api/docs/custom-agents): Define your own agents using `AGENTS.md` and `SKILL.md`.