"""Support library for the lite-auth-proxy end-to-end test suite.

These modules are intentionally separate from the Gherkin step definitions so
the step files stay readable. Nothing here is proxy-internal — the suite talks
to the proxy purely over HTTP, exactly like a real client would.
"""
