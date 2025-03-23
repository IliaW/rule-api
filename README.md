# rule-api

## Overview

The `rule-api` provides endpoints to check crawl permissions, and to `get`, `create`, `update`, and `delete` custom
rules.

![scheme](docs/scheme.png)

## Endpoints

### Swagger Documentation

- **GET** `/swagger/index.html` - Access the Swagger UI for API documentation.

### Health Check

- **GET** `/ping` - Check if the server is running.

### Crawl Permissions

The base URL for the API call is determined by the `UrlPath` configuration setting.

- **GET** `/crawl-allowed` - Check if crawling is allowed for a given domain by checking the `robots.txt` file.

### Custom Rules

Next calls require _**authentication**_.
Add `X-Api-Key` header to requests.

The base URL for the API calls is determined by the `UrlPath` configuration setting.

- **GET** `/custom-rule` - Retrieve custom rules for a domain.
- **POST** `/custom-rule` - Create a new custom rule.
- **PUT** `/custom-rule` - Update an existing custom rule.
- **DELETE** `/custom-rule` - Delete a custom rule.
