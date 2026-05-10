Feature: otters info

  As an operator I want to confirm the daemon I'm talking to and the
  configuration it booted with — without grepping logs.

  Scenario: reports a structured CLI + daemon block
    Given a fresh daemon
    When I run "otters info"
    Then the exit code is 0
    And the output reports both CLI and daemon sections

  Scenario: reports the executor backend the daemon was started with
    Given a fresh daemon
    When I run "otters info"
    Then the exit code is 0
    And the output reports the active executor

  Scenario: reports the socket the daemon is listening on
    Given a fresh daemon
    When I run "otters info"
    Then the exit code is 0
    And the output reports the daemon's socket
