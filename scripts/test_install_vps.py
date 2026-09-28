import os
import subprocess
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name('install-vps.sh')


class InstallVPSTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.bin_dir = self.root / 'bin'
        self.bin_dir.mkdir()
        self.install_dir = self.root / 'panel'
        self.log = self.root / 'commands.log'
        self.repo_url = 'https://github.com/xinxinxiaochenchen/xinchen-panel.git'
        git = self.bin_dir / 'git'
        git.write_text('''#!/bin/sh
printf 'git %s\\n' "$*" >> "$COMMAND_LOG"
case " $* " in
  *" clone "*)
    [ "${FAIL_CLONE:-0}" = 1 ] && exit 1
    eval "target=\\${$#}"
    mkdir -p "$target/.git" "$target/scripts"
    printf '#!/bin/sh\\nprintf "deploy %%s\\n" "$*" >> "$COMMAND_LOG"\\n' > "$target/scripts/deploy-vps.sh"
    ;;
  *" remote get-url origin "*) printf '%s\\n' "$EXPECTED_REPO" ;;
  *" status --porcelain "*) printf '%s' "${DIRTY_STATUS:-}" ;;
  *" rev-parse --show-toplevel "*) printf '%s\\n' "$XINCHEN_PANEL_DIR" ;;
  *" symbolic-ref --short HEAD "*) printf '%s\\n' "${CURRENT_BRANCH:-main}" ;;
  *" pull "*) [ "${FAIL_PULL:-0}" != 1 ] ;;
esac
''')
        git.chmod(0o755)
        docker = self.bin_dir / 'docker'
        docker.write_text('#!/bin/sh\n[ "${FAIL_DOCKER:-0}" != 1 ]\n')
        docker.chmod(0o755)
        self.env = os.environ.copy()
        self.env.update({
            'PATH': str(self.bin_dir) + os.pathsep + self.env['PATH'],
            'XINCHEN_PANEL_DIR': str(self.install_dir.resolve()),
            'COMMAND_LOG': str(self.log),
            'EXPECTED_REPO': self.repo_url,
        })

    def run_script(self, *args):
        return subprocess.run(['sh', str(SCRIPT), *args], env=self.env, text=True, capture_output=True)

    def commands(self):
        return self.log.read_text().splitlines() if self.log.exists() else []

    def test_fresh_install_clones_repository_and_starts_public_preview(self):
        result = self.run_script('--public-preview')
        self.assertEqual(result.returncode, 0, result.stderr)
        commands = self.commands()
        self.assertTrue(any('clone --branch main --single-branch' in command for command in commands), commands)
        self.assertEqual(commands[-1], 'deploy --public-preview')

    def test_existing_checkout_only_fast_forwards_and_keeps_loopback_default(self):
        (self.install_dir / '.git').mkdir(parents=True)
        (self.install_dir / 'scripts').mkdir()
        (self.install_dir / 'scripts/deploy-vps.sh').write_text('#!/bin/sh\nprintf "deploy %s\\n" "$*" >> "$COMMAND_LOG"\n')
        result = self.run_script()
        self.assertEqual(result.returncode, 0, result.stderr)
        commands = self.commands()
        self.assertTrue(any('pull --ff-only origin main' in command for command in commands), commands)
        self.assertEqual(commands[-1], 'deploy ')

    def test_existing_directory_without_checkout_is_not_overwritten(self):
        self.install_dir.mkdir()
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('not a checkout', result.stderr)
        self.assertFalse(any('clone' in command or 'deploy' in command for command in self.commands()))

    def test_dirty_checkout_stops_before_pull_and_deploy(self):
        (self.install_dir / '.git').mkdir(parents=True)
        self.env['DIRTY_STATUS'] = ' M README.md'
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('local changes', result.stderr)
        self.assertFalse(any('pull' in command or 'deploy' in command for command in self.commands()))

    def test_wrong_repository_is_rejected(self):
        (self.install_dir / '.git').mkdir(parents=True)
        self.env['EXPECTED_REPO'] = 'https://github.com/example/other.git'
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('different repository', result.stderr)
        self.assertFalse(any('pull' in command or 'deploy' in command for command in self.commands()))

    def test_non_main_branch_is_rejected(self):
        (self.install_dir / '.git').mkdir(parents=True)
        self.env['CURRENT_BRANCH'] = 'custom-work'
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('main branch', result.stderr)
        self.assertFalse(any('pull' in command or 'deploy' in command for command in self.commands()))

    def test_clone_failure_stops_deployment(self):
        self.env['FAIL_CLONE'] = '1'
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('clone', result.stderr.lower())
        self.assertFalse(any('deploy' in command for command in self.commands()))

    def test_pull_failure_stops_deployment(self):
        (self.install_dir / '.git').mkdir(parents=True)
        self.env['FAIL_PULL'] = '1'
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('update', result.stderr.lower())
        self.assertFalse(any('deploy' in command for command in self.commands()))

    def test_unusable_docker_stops_before_checkout_changes(self):
        self.env['FAIL_DOCKER'] = '1'
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Docker', result.stderr)
        self.assertFalse(self.commands())

    def test_help_does_not_require_docker_or_change_checkout(self):
        self.env['FAIL_DOCKER'] = '1'
        result = self.run_script('--help')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('Usage:', result.stdout)
        self.assertFalse(self.commands())


if __name__ == '__main__':
    unittest.main()
