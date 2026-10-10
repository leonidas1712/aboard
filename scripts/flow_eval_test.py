"""Protect the evaluator from scoring human output or terminal redraws as success."""
import unittest
import tempfile
import os
import subprocess
from pathlib import Path
from flow_eval import friction, hello_from, texts


class ReportTests(unittest.TestCase):
    def test_person_or_other_agent_cannot_satisfy_hello(self):
        messages = [{'from': {'kind': 'person', 'name': 'leo'}, 'body': 'hello'},
                    {'from': {'kind': 'agent', 'name': 'other'}, 'body': 'hello'}]
        self.assertFalse(hello_from(messages, {'claude'}))
        messages.append({'from': {'kind': 'agent', 'name': 'claude'}, 'body': 'Hello Maya'})
        self.assertTrue(hello_from(messages, {'claude'}))

    def test_hi_from_the_expected_agent_is_a_greeting(self):
        self.assertTrue(hello_from([{'from': {'kind': 'agent', 'name': 'claude'},
                                    'body': 'Hi Maya, welcome to the board.'}], {'claude'}))

    def test_friction_uses_assistant_text_and_shell_calls_not_tool_echo(self):
        rows = [
            {'message': {'role': 'assistant', 'content': [
                {'type': 'text', 'text': "I'm not sure. Which name?"},
                {'type': 'tool_use', 'input': {'command': 'aboard status'}}]}},
            {'message': {'role': 'user', 'content': 'Which name?'}},
            {'type': 'response_item', 'payload': {'type': 'function_call',
                'arguments': '{"cmd":"aboard status"}'}},
            {'message': {'role': 'user', 'content': [
                {'type': 'tool_result', 'is_error': True, 'content': 'Error (refused): no'}]}}]
        report = friction(rows)
        self.assertEqual(report['assistant_questions'], 1)
        self.assertEqual(report['uncertainty_phrases'], 1)
        self.assertEqual(report['repeated_commands'], 1)
        self.assertEqual(report['error_records'], 1)

    def test_codex_custom_exec_records_command_calls(self):
        row = {'payload': {'type': 'custom_tool_call', 'name': 'exec',
               'input': 'const r = await tools.exec_command({cmd:"aboard status",yield_time_ms:1000}); text(r.output);'}}
        self.assertEqual(texts(row)[1], ['aboard status'])

    def test_login_shell_keeps_private_install_path(self):
        from flow_eval import install_shell_environment
        with tempfile.TemporaryDirectory() as root:
            home = Path(root)
            tools = home / 'tools'
            tools.mkdir()
            (tools / 'curl').write_text('#!/bin/sh\nprintf private-install\n')
            (tools / 'curl').chmod(0o700)
            env = dict(os.environ, HOME=str(home), PATH=str(tools)+':/usr/bin:/bin',
                       ABOARD_INSTALL_FROM=str(home/'dev-build'))
            install_shell_environment(home, env)
            result = subprocess.run(['/bin/zsh','-lc','command -v curl; printf "%s" "$ABOARD_INSTALL_FROM"'],
                                    env=env, capture_output=True, text=True, check=True)
            self.assertEqual(result.stdout.splitlines()[0], str(tools/'curl'))
            self.assertEqual(result.stdout.splitlines()[1], str(home/'dev-build'))

    def test_wrong_installed_build_refuses_a_flow_result(self):
        from flow_eval import check_installed_build
        with tempfile.TemporaryDirectory() as root:
            home = Path(root)
            binary = home / 'dev-build'
            binary.write_bytes(b'dev')
            check_installed_build(home, binary)
            installed = home / '.local/bin/aboard'
            installed.parent.mkdir(parents=True)
            installed.write_bytes(b'release')
            with self.assertRaisesRegex(RuntimeError, 'different build'):
                check_installed_build(home, binary)
            installed.write_bytes(b'dev')
            check_installed_build(home, binary)


if __name__ == '__main__':
    unittest.main()
