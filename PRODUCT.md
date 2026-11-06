# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Stack

Go backend, React + TypeScript + Vite frontend, PostgreSQL and Docker Compose. The user chose PostgreSQL and Docker and delegated the remaining stack selection.

## Users

A developer working on a local macOS project, assigning and monitoring coding agents.

## Product Purpose

Make it possible to submit work to a coding agent, follow its real progress, review results and respond to approvals from an accessible web interface.

## Operating Context

The backend runs locally alongside Codex and the user's project. PostgreSQL runs via Docker Compose. V1 starts with Codex; other providers and handoffs are planned for a later version.

## Capabilities and Constraints

Task status, events and approvals must come from the backend. Do not invent agent activity. A project permits one active task at a time. Cancellation does not undo file changes.

## Brand Commitments

Pixel Art with the atmosphere of an adventurers' guild. The guild visually represents the agents' actual work.

## Evidence on Hand

Feature and architecture specifications are in `docs/`. No real users, testimonials or usage metrics have been supplied.

## Accessibility & Inclusion

Primary controls support keyboard and screen readers; states must not rely on color alone and motion must respect user preferences.
