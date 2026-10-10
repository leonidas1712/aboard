"""Protect the evaluator from scoring human output or terminal redraws as success."""
import unittest
from flow_eval import friction, hello_from


class ReportTests(unittest.TestCase):
    def test_person_or_other_agent_cannot_satisfy_hello(self):
        messages = [{'from': {'kind': 'person', 'name': 'leo'}, 'body': 'hello'},
                    {'from': {'kind': 'agent', 'name': 'other'}, 'body': 'hello'}]
        self.assertFalse(hello_from(messages, {'claude'}))
        messages.append({'from': {'kind': 'agent', 'name': 'claude'}, 'body': 'Hello Maya'})
        self.assertTrue(hello_from(messages, {'claude'}))

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


if __name__ == '__main__':
    unittest.main()
