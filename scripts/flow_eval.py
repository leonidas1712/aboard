#!/usr/bin/env python3
"""Run human-driven team flows in private interactive harness sessions.

Reports preserve failed flows instead of teaching the agents missing commands.
Subscription credentials and invitation handovers stay in the private run folder.
"""
import argparse
import ast
import hashlib
import json
import os
from pathlib import Path
import re
import selectors
import shlex
import shutil
import signal
import subprocess
import sys
import tempfile
import time

REPO = Path(__file__).resolve().parent.parent
SCENARIOS = ('invite', 'existing-member', 'own-session', 'interrupted')
ISOLATION = ('HOME', 'ABOARD_HOME', 'CODEX_HOME', 'CLAUDE_CONFIG_DIR',
             'SSL_CERT_FILE', 'PATH', 'ABOARD_SANDBOX_NAME',
             'ABOARD_SANDBOX_TEAM_URL', 'ABOARD_INSTALL_FROM',
             'ABOARD_SANDBOX_INSTALL_SCRIPT', 'DISABLE_AUTOUPDATER')


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2) + '\n')
    path.chmod(0o600)


def clean_env():
    return {k: v for k, v in os.environ.items()
            if k not in ('ZDOTDIR', 'BASH_ENV', 'ENV') and
            (not k.startswith(('ABOARD', 'CODEX', 'CLAUDE', 'OMP', 'PI_'))
             or k == 'CLAUDE_CODE_OAUTH_TOKEN')}


def install_shell_environment(home, env):
    """Keep the private tool path after a harness starts a login shell."""
    home = Path(home)
    exports = '\n'.join('export ' + key + '=' + shlex.quote(env[key])
                        for key in ISOLATION if key in env) + '\n'
    for name in ('.zshenv', '.zprofile', '.zlogin', '.bash_profile', '.bashrc'):
        path = home / name
        if path.is_symlink():
            raise RuntimeError('Refuse a symlinked private shell profile.')
        path.write_text(exports)
        path.chmod(0o600)


def checksum(path):
    if not path.exists():
        return 'absent'
    if path.is_symlink():
        return 'link:' + os.readlink(path)
    if path.is_dir():
        rows = [(str(p.relative_to(path)), checksum(p))
                for p in sorted(path.rglob('*')) if not p.is_dir()]
        return hashlib.sha256(json.dumps(rows).encode()).hexdigest()
    return hashlib.sha256(path.read_bytes()).hexdigest()


def check_installed_build(home, binary):
    installed = Path(home) / '.local/bin/aboard'
    if installed.exists() and checksum(installed) != checksum(Path(binary)):
        raise RuntimeError('Fixture installed a different build; this is not a valid flow result.')


def protected():
    home = Path(os.environ['HOME'])
    paths = [home / p for p in ('.claude/settings.json', '.claude/hooks',
             '.codex/config.toml', '.codex/hooks.json', '.codex/auth.json',
             '.aboard/config.json', '.aboard/credentials.json', '.aboard/servers.json')]
    for key in ('CODEX_HOME', 'CLAUDE_CONFIG_DIR'):
        if os.environ.get(key):
            paths.extend(Path(os.environ[key]) / p for p in
                         ('config.toml', 'hooks.json', 'auth.json', 'settings.json'))
    config = Path(os.environ.get('ABOARD_HOME', str(home / '.config/aboard')))
    if os.environ.get('ABOARD_HOME'):
        config = config / 'config'
    paths.extend(config / p for p in ('credentials.json', 'servers.json', 'config.json'))
    return {str(p): checksum(p) for p in paths}


def texts(value):
    """Extract assistant prose and shell calls without counting echoed tool output."""
    if not isinstance(value, dict):
        return [], []
    messages, commands = [], []
    msg = value.get('message', {})
    if isinstance(msg, dict) and msg.get('role') == 'assistant':
        for block in msg.get('content', []):
            if isinstance(block, dict):
                if block.get('type') == 'text':
                    messages.append(block.get('text', ''))
                elif block.get('type') == 'tool_use':
                    command = block.get('input', {}).get('command')
                    if command:
                        commands.append(command)
    payload = value.get('payload', {})
    if isinstance(payload, dict):
        if payload.get('type') == 'message' and payload.get('role') == 'assistant':
            messages.extend(c.get('text', '') for c in payload.get('content', [])
                            if isinstance(c, dict))
        if payload.get('type') == 'custom_tool_call' and payload.get('name') == 'exec':
            source = payload.get('input', '')
            for literal in re.findall(r'''\bcmd\s*:\s*("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*')''', source):
                try:
                    commands.append(ast.literal_eval(literal))
                except (ValueError, SyntaxError):
                    pass
        if payload.get('type') == 'function_call':
            try:
                args = json.loads(payload.get('arguments', '{}'))
                command = args.get('cmd', args.get('command'))
                if command:
                    commands.append(str(command))
            except (ValueError, TypeError):
                pass
    return messages, commands


def friction(records):
    prose, commands, errors = [], [], 0
    for row in records:
        messages, calls = texts(row)
        prose.extend(messages)
        commands.extend(calls)
        raw = json.dumps(row)
        # Count transcript records, not occurrences in terminal redraws.
        if re.search(r'Error \([a-z_]+\)|"is_error": true|Process exited with code [1-9]', raw):
            errors += 1
    body = '\n'.join(prose)
    return {'error_records': errors,
            'repeated_commands': len(commands) - len(set(commands)),
            'assistant_questions': body.count('?'),
            'uncertainty_phrases': len(re.findall(r"\b(?:I'm not sure|I am not sure|perhaps|might need|maybe)\b", body, re.I)),
            'command_calls': len(commands),
            'heuristics_require_transcript_review': True}


def hello_from(messages, names):
    return any(m.get('from', {}).get('kind') == 'agent' and
               m['from'].get('name') in names and
               re.search(r'\bhello\b', m.get('body', ''), re.I)
               for m in messages)


class Eval:
    def __init__(self, args):
        self.args = args
        self.root = Path(args.output or tempfile.mkdtemp(prefix='aboard-flow-', dir='/tmp')).resolve()
        self.root.mkdir(mode=0o700, parents=True, exist_ok=True)
        if any(self.root.iterdir()):
            raise RuntimeError('Choose an empty output folder; existing runs are never overwritten.')
        self.root.chmod(0o700)
        self.home = self.root / 'fixture-home'
        self.home.mkdir(mode=0o700)
        self.env = clean_env()
        self.env.pop('XDG_CONFIG_HOME', None)
        self.env.pop('XDG_DATA_HOME', None)
        self.env.pop('XDG_STATE_HOME', None)
        codex_path = shutil.which(args.codex)
        if codex_path:
            self.env['PATH'] = str(Path(codex_path).parent) + os.pathsep + self.env['PATH']
        self.env.update(HOME=str(self.home), ABOARD_SANDBOXES=str(self.root / 'sandboxes'),
                        ABOARD_EXIT_WITH_PID=str(os.getpid()))
        self.team = self.root / 'sandboxes/teams/qa'
        self.binary = Path(os.environ.get('SANDBOX_ABOARD', str(REPO / '.bin/aboard'))).resolve()
        self.helper = os.environ.get('SANDBOX_TEAM_HELPER', str(REPO / '.bin/sandbox-team'))
        self.env.update(SANDBOX_ABOARD=str(self.binary), SANDBOX_TEAM_HELPER=self.helper)
        self.socket = str(self.root / 'tmux.sock')
        self.sides = {}
        self.before = protected()
        self.prompts = []
        self.start = time.monotonic()
        self.started = False
        self.seen_files = {}
        self.records = {'leo': [], 'maya': []}
        self.responded = set()
        self.trusted = {}
        self.last_members = None

    def run(self, argv, env=None, check=True, cwd=None):
        return subprocess.run(argv, env=env or self.env, cwd=cwd or REPO,
                              capture_output=True, text=True, timeout=60, check=check)

    def sandbox(self, person, command=':', fresh=False):
        env = dict(self.env, TEAM='qa', FRESH='1' if fresh else '0', SANDBOX_CMD=command)
        return self.run(['sh', str(REPO / 'scripts/sandbox'), 'open', person], env=env)

    def prepare(self):
        # Copy only login material, never the source config, hooks or skills.
        source = Path(os.environ.get('CODEX_HOME', str(Path(os.environ['HOME']) / '.codex'))) / 'auth.json'
        if source.is_file():
            target = self.home / '.codex/auth.json'
            target.parent.mkdir(mode=0o700)
            shutil.copyfile(source, target)
            target.chmod(0o600)
        self.started = True
        self.run(['sh', str(REPO / 'scripts/sandbox'), 'team-start', 'qa'])
        for side in ('leo', 'maya'):
            folder = self.team / 'people' / side
            path = folder / 'environment.json'
            code = ('import os,json; from pathlib import Path; '
                    f'Path({str(path)!r}).write_text(json.dumps({{k:os.environ[k] for k in {ISOLATION!r} if k in os.environ}}))')
            self.sandbox(side, 'python3 -c ' + shlex.quote(code), fresh=side == 'maya')
            env = dict(self.env, **json.loads(path.read_text()))
            self.sides[side] = {'folder': folder, 'env': env, 'project': folder / 'project'}
            install_shell_environment(Path(env['HOME']), env)
        self.person('leo', 'board', 'new', 'qa', '--title', 'Flow QA', '--server', 'qa')
        if self.args.approval == 'auto':
            self.person('leo', 'allowance', 'on', '--server', 'qa')
            self.person('leo', 'allowance', 'set', 'invite-people', 'on', '--server', 'qa')
        if self.args.scenario == 'existing-member':
            invite = self.person('leo', 'invite', '--person', '--server', 'qa')
            self.sandbox('maya', 'curl -fsSL https://comeaboard.dev/install | sh; aboard connect ' +
                         shlex.quote(invite['link']) + ' --handle maya --server-name qa --json', fresh=True)
        elif self.args.scenario == 'own-session':
            source = self.sides['leo']['folder'] / 'aboard-home/config'
            dest = self.sides['maya']['folder'] / 'aboard-home/config'
            dest.mkdir(mode=0o700, exist_ok=True)
            # This scenario is another session of the same already signed-in person.
            for entry in source.iterdir():
                if entry.is_file() and entry.suffix == '.json':
                    shutil.copyfile(entry, dest / entry.name)
                    (dest / entry.name).chmod(0o600)
        fresh = not (self.sides['maya']['folder'] / 'home/.local/bin/aboard').exists()
        write_json(self.root / 'prepare.json', {'fresh': fresh,
                   'project': str(self.sides['maya']['project']),
                   'source_unchanged': protected() == self.before})

    def person(self, side, *args, check=True):
        env = dict(self.sides[side]['env'])
        for key in list(env):
            if key.startswith(('ABOARD_SESSION', 'ABOARD_AGENT', 'CODEX_THREAD', 'CLAUDE_SESSION')):
                del env[key]
        result = self.run([str(self.binary), *args, '--json'], env, check=check,
                          cwd=self.sides[side]['project'])
        if result.returncode:
            return None
        return json.loads(result.stdout)

    def tmux(self, *args, check=True):
        return self.run(['tmux', '-u', '-S', self.socket, *args], check=check).stdout

    def start_side(self, side):
        info = self.sides[side]
        env = info['env']
        if side == 'leo':
            config = Path(env['CLAUDE_CONFIG_DIR']) / '.claude.json'
            write_json(config, {'hasCompletedOnboarding': True, 'theme': 'dark',
                       'projects': {str(info['project']): {'hasTrustDialogAccepted': True}}})
            argv = [self.args.claude, '--model', self.args.models[0], '--allowedTools', 'Bash']
        else:
            config = Path(env['CODEX_HOME']) / 'config.toml'
            config.write_text('check_for_update_on_startup = false\n'
                              '[sandbox_workspace_write]\nnetwork_access = true\n' +
                              '[projects.' + json.dumps(str(info['project'])) + ']\ntrust_level = "trusted"\n')
            config.chmod(0o600)
            argv = [self.args.codex, '-m', self.args.models[1], '-s', 'workspace-write',
                    '-a', 'never', '--add-dir', str(self.root)]
        info['argv'] = argv
        launch = self.root / (side + '-launch.json')
        write_json(launch, {'env': {k: env[k] for k in ISOLATION if k in env},
                           'argv': argv, 'cwd': str(info['project'])})
        cmd = shlex.join([sys.executable, str(Path(__file__).resolve()), '--launch', str(launch)])
        if side == 'leo':
            self.tmux('-f', '/dev/null', 'new-session', '-d', '-s', 'flow', '-n', side,
                      '-x', '180', '-y', '50', cmd)
            self.tmux('set-option', '-g', 'remain-on-exit', 'on')
            self.tmux('set-option', '-g', 'history-limit', '200000')
        else:
            self.tmux('new-window', '-t', 'flow', '-n', side, cmd)
        self.tmux('pipe-pane', '-t', 'flow:' + side, 'cat >> ' + shlex.quote(str(self.root / (side + '-terminal.log'))))

    def prompt(self, side, text):
        self.prompts.append({'side': side, 'text': text, 'elapsed': time.monotonic() - self.start})
        self.tmux('set-buffer', '--', text)
        self.tmux('paste-buffer', '-t', 'flow:' + side, '-p')
        self.tmux('send-keys', '-t', 'flow:' + side, 'Enter')

    def screen(self, side):
        return self.tmux('capture-pane', '-p', '-J', '-S', '-', '-t', 'flow:' + side)

    def collect(self):
        for side, info in self.sides.items():
            for folder in ('claude-code/projects', 'codex/sessions'):
                for path in (info['folder'] / folder).rglob('*.jsonl'):
                    raw = path.read_text(errors='replace')
                    self.seen_files[str(path)] = raw
            rows = []
            for path, raw in self.seen_files.items():
                if str(info['folder']) not in path:
                    continue
                for line in raw.splitlines():
                    try:
                        rows.append(json.loads(line))
                    except ValueError:
                        pass
            self.records[side] = rows

    def observe(self):
        people = self.person('leo', 'people', '--server', 'qa', check=False)
        members = self.person('leo', 'board', 'people', '--board', 'qa', '--server', 'qa', check=False)
        board = self.person('leo', 'read', '--board', 'qa', '--server', 'qa', '--limit', '200', check=False)
        write_json(self.root / 'board.json', board)
        write_json(self.root / 'people.json', people)
        write_json(self.root / 'members.json', members)
        if self.last_members != members:
            self.last_members = members
            write_json(self.root / 'audit.json', self.person('leo', 'audit', 'verify', '--board', 'qa', '--server', 'qa', check=False))
        expected = 'leo' if self.args.scenario == 'own-session' else 'maya'
        person_rows = (people or {}).get('people', [])
        found = any(p.get('handle') == expected for p in person_rows)
        groups = (members or {}).get('people', [])
        agents = [a for p in groups if p.get('handle') == expected for a in p.get('agents', [])]
        messages = (board or {}).get('messages', [])
        leo_names = {a['name'] for p in groups if p.get('handle') == 'leo' for a in p.get('agents', []) if a.get('harness') == 'claude-code'}
        maya_names = {a['name'] for a in agents if a.get('harness') == 'codex'}
        leo_hello = hello_from(messages, leo_names)
        maya_hello = hello_from(messages, maya_names)
        result = {'person_exists': found, 'agent_on_board': bool(maya_names),
                  'hello_from_admin_agent': leo_hello, 'hello_from_second_agent': maya_hello}
        return result

    def preflight(self):
        version = self.run([self.args.codex, '--version']).stdout.strip()
        match = re.search(r'(\d+)\.(\d+)\.(\d+)', version)
        if not match or tuple(map(int, match.groups())) < (0, 160, 0):
            raise RuntimeError('Select Codex 0.160 or newer with FLOW_CODEX; the eval requires real hooks.')
        write_json(self.root / 'versions.json', {'codex': version,
                   'claude': self.run([self.args.claude, '--version']).stdout.strip()})

    def trust_hooks(self):
        info = self.sides['maya']
        hooks = info['project'] / '.codex/hooks.json'
        if not hooks.exists():
            return
        digest = checksum(hooks)
        if self.trusted.get('maya') == digest:
            return
        requests = [
            {'id': 1, 'method': 'initialize', 'params': {'clientInfo': {'name': 'aboard-flow-eval', 'version': '0'}}},
            {'method': 'initialized'},
            {'id': 2, 'method': 'hooks/list', 'params': {'cwds': [str(info['project'])]}}]
        proc = subprocess.Popen([self.args.codex, 'app-server'], cwd=info['project'],
                                env=info['env'], stdin=subprocess.PIPE,
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        try:
            proc.stdin.write(''.join(json.dumps(row) + '\n' for row in requests))
            proc.stdin.flush()
            selector = selectors.DefaultSelector()
            selector.register(proc.stdout, selectors.EVENT_READ)
            end = time.monotonic() + 30
            while time.monotonic() < end:
                if not selector.select(max(0, end - time.monotonic())):
                    break
                line = proc.stdout.readline()
                if not line:
                    break
                row = json.loads(line)
                if row.get('id') != 2:
                    continue
                entries = []
                for data in row.get('result', {}).get('data', []):
                    for hook in data.get('hooks', []):
                        if hook.get('source') == 'project' and hook.get('currentHash'):
                            entries.append('[hooks.state.' + json.dumps(hook['key']) + ']\ntrusted_hash = ' + json.dumps(hook['currentHash']) + '\n')
                if not entries:
                    break
                config = Path(info['env']['CODEX_HOME']) / 'config.toml'
                with config.open('a') as f:
                    f.write(''.join(entries))
                self.trusted['maya'] = digest
                return
            raise RuntimeError('Codex did not report the project hook hashes for trust.')
        finally:
            proc.terminate()
            try:
                proc.communicate(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.communicate()


    def wait_prompt(self, side):
        end = time.monotonic() + 60
        while time.monotonic() < end:
            screen = self.screen(side)
            marker = '❯' if side == 'leo' else '›'
            if marker in screen and not any(word in screen for word in ('Resuming session', 'esc to interrupt')):
                return
            time.sleep(0.5)
        raise RuntimeError(side + ' did not resume to its prompt.')

    def drive(self):
        self.preflight()
        for side in ('leo', 'maya'):
            self.start_side(side)
        deadline = time.monotonic() + self.args.timeout
        initial = False
        handed = False
        interrupted = False
        while time.monotonic() < deadline:
            check_installed_build(self.sides['maya']['env']['HOME'], self.binary)
            self.collect()
            screens = {side: self.screen(side) for side in self.sides}
            self.trust_hooks()
            # Harness trust is fixture setup, not help with the team flow.
            for side, screen in screens.items():
                if 'Hooks need review' in screen and 'Trust all and continue' in screen:
                    self.tmux('send-keys', '-t', 'flow:' + side, '2', 'Enter')
                elif 'Yes, I trust this folder' in screen:
                    self.tmux('send-keys', '-t', 'flow:' + side, 'Down', 'Enter')
            if not initial and '❯' in screens['leo'] and ('›' in screens['maya'] or 'codex' in screens['maya'].lower()):
                request = 'Bring maya onto the qa board. She will start a fresh Codex session. Give me the invitation link and the prompt to paste into her agent, then say hello to her there.'
                if self.args.scenario == 'existing-member':
                    request = 'Maya is already a person on this server. Bring her onto the qa board and give me the prompt for her Codex session, then say hello to her there.'
                elif self.args.scenario == 'own-session':
                    request = 'Bring my other Codex session onto the qa board. Give me the prompt to paste there, then say hello to that agent.'
                self.prompt('leo', request)
                initial = True
            if not initial:
                time.sleep(0.5)
                continue
            approvals = self.person('leo', 'approvals', '--server', 'qa', check=False) or {}
            for approval in approvals.get('approvals', []):
                aid = approval.get('id')
                if approval.get('state') == 'pending' and aid not in self.responded:
                    decision = self.person('leo', 'approvals', 'allow', aid, '--server', 'qa')
                    self.prompt('leo', 'I allowed that request in my terminal. Here is the result; give me the handover for Maya:\n' + json.dumps(decision))
                    self.responded.add(aid)
            if not handed:
                answers = [text for row in self.records['leo'] for text in texts(row)[0]]
                prose = '\n'.join(answers)
                # Only copy the agent's final handover, never manufacture an invitation.
                links = re.findall(r'https?://[^\s`<>\)]+/join#[A-Za-z0-9_-]+', prose)
                join = re.search(r'Join Aboard board .+? with code [^\s`]+', prose)
                if links or join:
                    handover = next(text for text in reversed(answers) if (links[-1] in text if links else join.group(0) in text))
                    name = 'leo' if self.args.scenario == 'own-session' else 'maya'
                    prompt = 'My handle is ' + name + '. Please follow this invitation and say hello on qa:\n' + handover
                    if self.args.scenario == 'interrupted':
                        prompt += '\nStop after saving the first setup step; I need to close this session, then continue it later.'
                    self.prompt('maya', prompt)
                    handed = True
            if handed and self.args.scenario == 'interrupted' and not interrupted:
                saved = list((self.sides['maya']['folder'] / 'aboard-home/state/onboarding').glob('*.json'))
                if saved:
                    self.restart('maya')
                    self.wait_prompt('maya')
                    self.prompt('maya', 'Continue the setup you saved, then say hello on qa.')
                    interrupted = True
            # Restarts are only made after the agent requests one, never to repair a timeout.
            for side in self.sides:
                answers = [text for row in self.records[side] for text in texts(row)[0]]
                prose = answers[-1] if answers else ''
                marker = (side, 'restart')
                if re.search(r'\brestart\b.{0,80}\b(?:session|Codex|Claude|harness)\b', prose, re.I) and marker not in self.responded:
                    self.responded.add(marker)
                    self.restart(side)
                    self.wait_prompt(side)
                    self.prompt(side, 'I restarted this session as requested. Please continue.')
            result = self.observe()
            if all(result.values()):
                if self.args.scenario != 'interrupted' or interrupted:
                    check_installed_build(self.sides['maya']['env']['HOME'], self.binary)
                    return result
            time.sleep(1)
        raise RuntimeError('The flow did not reach the required end state before the deadline.')

    def restart(self, side):
        info = self.sides[side]
        self.collect()
        ids = []
        for row in self.records[side]:
            if row.get('sessionId'):
                ids.append(row['sessionId'])
            if row.get('type') == 'session_meta' and row.get('payload', {}).get('id'):
                ids.append(row['payload']['id'])
        if not ids:
            raise RuntimeError(side + ' asked to restart but no resumable session id was found.')
        self.tmux('send-keys', '-t', 'flow:' + side, 'C-c', 'C-c')
        launch = self.root / (side + '-launch.json')
        config = json.loads(launch.read_text())
        config['argv'] = info['argv'] + ['--resume', ids[-1]] if side == 'leo' else [self.args.codex, 'resume', '-m', self.args.models[1], '-s', 'workspace-write', '-a', 'never', '--add-dir', str(self.root), ids[-1]]
        write_json(launch, config)
        cmd = shlex.join([sys.executable, str(Path(__file__).resolve()), '--launch', str(launch)])
        self.tmux('respawn-pane', '-k', '-t', 'flow:' + side, cmd)

    def finish(self, failure=None):
        self.collect()
        for side in self.sides:
            for folder in ('claude-code/projects', 'codex/sessions'):
                source = self.sides[side]['folder'] / folder
                for path in source.rglob('*.jsonl'):
                    target = self.root / 'transcripts' / side / path.relative_to(source)
                    target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
                    shutil.copyfile(path, target)
                    target.chmod(0o600)
        if self.started:
            if shutil.which('tmux'):
                self.run(['tmux', '-S', self.socket, 'kill-server'], check=False)
            for side in self.sides:
                self.run([str(self.binary), 'down'], env=self.sides[side]['env'], check=False)
            self.run(['sh', str(REPO / 'scripts/sandbox'), 'team-stop', 'qa'], check=False)
        changed = [path for path, before in self.before.items() if checksum(Path(path)) != before]
        if changed:
            failure = 'Protected source files changed.'
        write_json(self.root / 'human-prompts.json', self.prompts)
        write_json(self.root / 'report.json', {'scenario': self.args.scenario,
                   'models': self.args.models, 'approval': self.args.approval,
                   'pass': failure is None, 'failure': failure,
                   'wall_seconds': round(time.monotonic() - self.start, 2),
                   'friction': {s: friction(r) for s, r in self.records.items()},
                   'protected_files_changed': changed, 'developer_judgment': 'pending transcript review'})
        if changed:
            raise RuntimeError('Protected source files changed; see the private report.')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--scenario', choices=SCENARIOS, default='invite')
    parser.add_argument('--models', default='haiku,gpt-6-luna')
    parser.add_argument('--approval', choices=('manual', 'auto'), default='manual')
    parser.add_argument('--output')
    parser.add_argument('--timeout', type=int, default=600)
    parser.add_argument('--prepare-only', action='store_true')
    parser.add_argument('--launch')
    parser.add_argument('--codex', default=os.environ.get('FLOW_CODEX', 'codex'))
    parser.add_argument('--claude', default=os.environ.get('FLOW_CLAUDE', 'claude'))
    args = parser.parse_args()
    if args.launch:
        config = json.loads(Path(args.launch).read_text())
        env = dict(clean_env(), **config['env'])
        os.chdir(config['cwd'])
        os.execvpe(config['argv'][0], config['argv'], env)
    args.models = args.models.split(',')
    if len(args.models) != 2 or args.timeout <= 0:
        parser.error('Use exactly two comma-separated models and a positive timeout.')
    run = Eval(args)
    failure = None
    signal.signal(signal.SIGTERM, lambda *_: sys.exit(143))
    try:
        run.prepare()
        if not args.prepare_only:
            if not os.environ.get('CLAUDE_CODE_OAUTH_TOKEN'):
                raise RuntimeError('Export CLAUDE_CODE_OAUTH_TOKEN before the live evaluation.')
            result = run.drive()
            write_json(run.root / 'end-state.json', result)
    except (Exception, KeyboardInterrupt, SystemExit) as exc:
        failure = str(exc)
    finally:
        run.finish(failure)
    print('Flow eval ' + ('PASS' if failure is None else 'FAIL') + ': ' + str(run.root))
    return 0 if failure is None else 1


if __name__ == '__main__':
    sys.exit(main())
