---
id: providers-aws
title: AWS
slug: /providers/aws
sidebar_position: 20
---

# AWS

## Overview

Lists **EC2 instances** for an AWS account profile. Rows use `provider: aws` with name, primary IP, region, and zone (availability zone). Execution is via **SSH** to the instance IP.

## Minimal auth

- **AWS credential chain** for the chosen profile (shared `~/.aws/credentials`, SSO, env vars, instance role, etc.).
- **IAM:** `ec2:DescribeInstances` (e.g. `AmazonEC2ReadOnlyAccess` or a custom read-only policy).

## Config (YAML)

Example file: [examples/config/aws.yaml](https://github.com/shareed2k/honey/blob/main/examples/config/aws.yaml)

```yaml
backends:
  aws:
    - name: aws-prod
      profile: production    # required — AWS shared config profile name
      region: us-east-1      # optional; default from profile/env
```

Optional per-backend **`docker_discover`**.

## CLI (no config file)

| Flag | Purpose |
|------|---------|
| `--aws-profile` | AWS profile name |
| `--aws-region` | AWS region |

## Verify

```bash
honey search --provider aws --aws-profile production -o json
```

## Notes

- Instances without a public/private IP honey can reach will not be connectable.
- Profile name in YAML is required for configured backends; flag-only mode uses the default credential chain when profile is empty.
