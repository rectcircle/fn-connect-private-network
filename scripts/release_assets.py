"""Release-ID based asset operations, including drafts without Git tags."""
import json
from pathlib import Path
import subprocess
import time
from urllib.parse import quote


def run(*args):
    return subprocess.check_output(args, text=True).strip()


def find_release(repository, tag, command=run):
    pages = json.loads(command('gh', 'api', '--paginate', '--slurp', f'repos/{repository}/releases'))
    return next((r for page in pages for r in page if r['tag_name'] == tag), None)


def assets(repository, release_id, command=run):
    pages = json.loads(command('gh', 'api', '--paginate', '--slurp',
                               f'repos/{repository}/releases/{release_id}/assets'))
    return [asset for page in pages for asset in page]


def download_asset(repository, asset, destination):
    name = asset['name']
    if not name or name in ('.', '..') or Path(name).name != name or '\\' in name:
        raise ValueError('Invalid remote release asset name')
    target = Path(destination) / name
    with target.open('xb') as output:
        subprocess.run(['gh', 'api', f"repos/{repository}/releases/assets/{asset['id']}",
                        '-H', 'Accept: application/octet-stream'], stdout=output, check=True)


def download_release(repository, tag, destination, name=None):
    release = find_release(repository, tag)
    if release is None:
        raise SystemExit('Release not found: ' + tag)
    selected = assets(repository, release['id'])
    if name is not None:
        selected = [a for a in selected if a['name'] == name]
        if len(selected) != 1:
            raise SystemExit('Missing or duplicate release asset: ' + name)
    Path(destination).mkdir(parents=True, exist_ok=True)
    for asset in selected:
        download_asset(repository, asset, destination)
    return release


def upload_asset(repository, release_id, name, source, command=run):
    # Use the ID rather than GET /releases/tags/:tag, which excludes tagless drafts.
    return command('gh', 'api',
                   f'https://uploads.github.com/repos/{repository}/releases/{release_id}/assets?name={quote(name, safe="")}',
                   '--method', 'POST', '-H', 'Content-Type: application/octet-stream', '--input', str(source))


def delete_draft_asset(repository, release_id, asset_id, command=run):
    """Retry a draft-only deletion, accepting a lost response only after listing."""
    for attempt in range(3):
        try:
            command('gh', 'api', f'repos/{repository}/releases/assets/{asset_id}', '--method', 'DELETE')
            return
        except subprocess.CalledProcessError:
            # A timeout may occur after GitHub already deleted the asset. A
            # successful list distinguishes that case from permission/API errors.
            if not any(a['id'] == asset_id for a in assets(repository, release_id, command)):
                return
            if attempt == 2:
                raise
            time.sleep(2 ** (attempt + 1))
