Feature: otters jobs (wire surface)

  As an operator I want the `otters jobs` CLI to report meaningful
  errors before I touch a real agent — so a typo at submit doesn't
  pollute history with a phantom job, and label filters compose
  predictably.

  These scenarios cover the wire/CLI surface only — they do NOT spin
  a live agent. Submit + watch + cancel against a real running agent
  is covered by the executor unit tests in internal/asyncjobs and
  agentfile/executor/{system,docker}; landing those flows here too
  would require the in-test agent build pipeline (see the @pending
  inheritance scenario), and is deferred until that exists.

  Scenario: ls reports nothing on a fresh daemon
    When I run "otters jobs ls"
    Then the exit code is 0
    And the output is empty

  Scenario: submit without --bin is rejected
    When I run "otters jobs run nope"
    Then the exit code is not 0
    And the stderr contains "bin"

  Scenario: submit against an unknown agent fails fast with NotFound
    When I run "otters jobs run nonexistent-agent --bin echo"
    Then the exit code is not 0
    And the stderr contains "not found"

  Scenario: cancel on an unknown job is a precondition failure
    When I run "otters jobs cancel job_does_not_exist"
    Then the exit code is not 0
    And the stderr contains "not currently running"

  Scenario: get on an unknown job returns NotFound
    When I run "otters jobs inspect job_does_not_exist"
    Then the exit code is not 0
    And the stderr contains "not found"

  Scenario: ls --label filter compiles and returns no false positives
    When I run "otters jobs ls --label io.openotters.session-id=does-not-exist"
    Then the exit code is 0
    And the output is empty

  Scenario: ls --label rejects malformed key=value
    When I run "otters jobs ls --label noequals"
    Then the exit code is not 0
    And the stderr contains "expected key=value"
