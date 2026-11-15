# Architecture

```mermaid
flowchart LR
    marketer(["Marketer"])

    subgraph web["web: nginx"]
        spa["Vue 3 SPA<br/>campaigns · composer · assistant chat · live delivery"]
    end

    subgraph api["api (cmd/api): Go, chi"]
        campaign["campaign<br/>lifecycle and rules"]
        segmentation["segmentation<br/>segment DSL → SQL, counts"]
        assistant["assistant<br/>tool loop, approvals"]
        progress["delivery<br/>progress, SSE"]
    end

    llm["Claude Messages API<br/>or offline mock"]
    db[("PostgreSQL 18<br/>customers, orders, campaigns,<br/>deliveries, River jobs")]

    subgraph worker["worker (cmd/worker): River, one process"]
        dispatch["dispatch_campaign<br/>resolve the audience"]
        send["send_message<br/>100/s per channel"]
        receipt["delivery_receipt"]
    end

    providers["SMS and email providers<br/>(mocked)"]
    seed["seed (cmd/seed)<br/>synthetic data, once"]

    marketer --> spa
    spa -->|"REST, SSE"| api
    assistant -->|"conversation and tools"| llm
    llm -.->|"tool calls"| assistant
    assistant -->|"validated tool calls"| campaign
    assistant -->|"estimates"| segmentation
    campaign -->|"reference checks"| segmentation
    campaign -->|"dispatch: status and job in one transaction"| db
    segmentation -->|"parameterised SQL"| db
    progress -->|"reads progress"| db
    worker -->|"jobs, recipients, statuses"| db
    dispatch --> send
    send --> providers
    providers -->|"receipts"| receipt
    seed --> db
```

- **Composing:** the SPA calls the API. Campaign rules live in `campaign`; segments compile to
  SQL in `segmentation`.
- **AI:** Claude only proposes tool calls. Go validates each one and runs it through the same
  services; a dispatch waits for a person's approval in the app.
- **Sending:** a dispatch commits the campaign's status and its River job together. The single
  worker resolves the recipients, sends through the rate-limited providers and records receipts;
  the campaign page follows progress over SSE.
