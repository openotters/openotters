Feature: Agent inheritance via FROM

  As an agent author I want to publish a base Agentfile to a registry
  and have child Agentfiles inherit from it via FROM, overriding only
  the fields I care about.

  Background:
    Given the otters daemon is running

  # @bug — currently fails with: "resolving manifest: latest:latest: not found"
  # The parent fetcher mishandles a tagged ref and ends up requesting
  # "latest:latest". This scenario should pass once the bug is fixed
  # in agentfile/store/load.go (or wherever the fetcher builds the
  # parent ref).
  #
  # Uses 127.0.0.1:5527 (the daemon's embedded loopback registry) so
  # the test stays offline — no GHCR auth, no network.
  Scenario: Child Agentfile overrides only the NAME
    Given a base agent image is published at "127.0.0.1:5527/agents/bdd-base:v1" with the Agentfile:
      """
      FROM scratch
      RUNTIME ghcr.io/openotters/runtime:latest
      MODEL anthropic/claude-sonnet-4-5-20250929
      NAME bdd-base
      """
    When I run an Agentfile named "bdd-hello-child" with the contents:
      """
      FROM 127.0.0.1:5527/agents/bdd-base:v1
      NAME bdd-hello-child
      """
    Then the agent "bdd-hello-child" should be running
    And the agent's runtime should be "ghcr.io/openotters/runtime:latest"
    And the agent's model should be "anthropic/claude-sonnet-4-5-20250929"
