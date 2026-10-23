#!/usr/bin/env python3
"""Download release assets by ID, including drafts without version tags."""
import argparse
from release_assets import download_release

if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('tag')
    parser.add_argument('directory')
    parser.add_argument('--repo', default='rectcircle/fn-connect-private-network')
    args = parser.parse_args()
    download_release(args.repo, args.tag, args.directory)
