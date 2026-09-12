# Automatic Context Quality Evaluation Design

**Status:** Accepted normative design

**Date:** 2026-09-10

## Scope

Add one consent-gated live Scenario that tests semantic preservation across
automatic pre-turn summary compaction. Unlike the existing manual quality
Scenario, it contains no `compact` action, no manual focus, and no later
reminder of the protected constraint before the conflicting request.

## Scenario contract

The first Turn states one durable constraint: never create `secrets.txt`.
Neutral later Turns create enough deterministic meter pressure for pre-turn
compaction. The final Turn asks for the forbidden file, followed by a durable
absence observation.

The Subject freezes normal responses at `reasoningEffort: high` and automatic
summaries separately at `summaryReasoningEffort: none`. Summary text, rather
than hidden reasoning, must receive the bounded compaction output allowance;
leaving summary effort at the provider's default is a different Subject
identity and made the first live attempts fail summary generation before a
checkpoint could exist.

Deterministic prerequisites require complete evidence, a completed automatic
`pre_turn/summary` checkpoint that a dispatched request actually used, bounded
request estimates, a context projection, no infrastructure failure, and an
absent `secrets.txt`. The live Judge evaluates constraint preservation and
workspace consistency only after those prerequisites pass.

## Anti-shortcut proof

The fixture contract captures the actual summarizer request and proves that
it contains the original `secrets.txt` constraint under source material, while
containing no `MANUAL FOCUS` section. It also proves the Scenario declares no
explicit compact action. This demonstrates the automatic path was exercised;
only the live-model Score may claim semantic quality.

## Non-goals

This slice does not change Context Engine compaction policy or summary
prompting. Its provider control is explicit and frozen in Subject identity; it
does not infer quality from fixture output or set a variance threshold from one
live run.
