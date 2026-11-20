# Control 8

Governance control plane for Kubernetes. Specs: `docs/specs/README.md`; build order: `docs/specs/07-roadmap.md`.

    make test    # unit tests
    make up      # build image, run on :8000 (config: deploy/control8.env, optional)
    make down

Status: M0 skeleton (health/ready endpoints, config, SQLite migrations).
