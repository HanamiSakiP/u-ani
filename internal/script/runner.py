"""BGmi script compatibility runner.

This file is embedded in the Go binary and executed with the system Python so
that existing BGmi Python scripts keep working without installing BGmi itself.
"""

import datetime
import glob
import importlib.util
import json
import os
import re
import sys
import types


def _install_shim():
    config = json.loads(os.environ.get("UANI_SCRIPT_CONFIG", "") or os.environ.get("BGMI_SCRIPT_CONFIG", "{}") or "{}")

    bgmi = types.ModuleType("bgmi")
    bgmi.__path__ = []
    script_mod = types.ModuleType("bgmi.script")
    utils_mod = types.ModuleType("bgmi.utils")
    config_mod = types.ModuleType("bgmi.config")
    lib_mod = types.ModuleType("bgmi.lib")
    lib_mod.__path__ = []
    fetch_mod = types.ModuleType("bgmi.lib.fetch")
    table_mod = types.ModuleType("bgmi.lib.table")
    constants_mod = types.ModuleType("bgmi.lib.constants")
    season_mod = types.ModuleType("bgmi.lib.season")
    website_mod = types.ModuleType("bgmi.website")
    website_mod.__path__ = []
    website_base = types.ModuleType("bgmi.website.base")
    website_model = types.ModuleType("bgmi.website.model")

    cfg = types.SimpleNamespace(**config)
    config_mod.cfg = cfg
    config_mod.BGMI_PATH = cfg.bgmi_path if hasattr(cfg, "bgmi_path") else config.get("bgmi_path", "")

    # --- episode parser -------------------------------------------------
    try:
        from anime_episode_parser import parse_episode as _parse_episode
    except Exception:  # pragma: no cover - only used on minimal Python installs
        _parse_episode = None

    _year_re = re.compile(r"[\[【(（]\s*(?:19|20)\d{2}\s*[\]】)）]")
    _sep_re = re.compile(r"[★☆](?:第\s*)?(?P<episode>0*[1-9]\d{0,2})(?=[\s(（)）\]】★☆_.])")

    def parse_episode(title):
        title = _year_re.sub("", str(title))
        if _parse_episode is not None:
            try:
                start, count = _parse_episode(title)
                if count == 1:
                    return start or 0
            except Exception:
                pass
        m = _sep_re.search(title)
        if m:
            return int(m.group("episode"))
        # Minimal fallback for environments without anime-episode-parser.
        candidates = re.findall(r"(?<!\d)(\d{1,4})(?!\d)", title)
        if candidates:
            return int(candidates[0])
        return 0

    utils_mod.parse_episode = parse_episode

    # --- model classes --------------------------------------------------
    class Episode:
        def __init__(self, title="", download="", episode=0, time=0,
                     subtitle_group=None, name=""):
            self.title = title
            self.download = download
            self.episode = episode
            self.time = time
            self.subtitle_group = subtitle_group
            self.name = name

    class SubtitleGroup:
        def __init__(self, id="", name=""):
            self.id = id
            self.name = name

    class WebsiteBangumi:
        def __init__(self, id="", update_day="Unknown", name="", status=0,
                     subtitle_group=None, cover="", episodes=None):
            self.id = id
            self.update_day = update_day
            self.name = name
            self.status = status
            self.subtitle_group = subtitle_group or []
            self.cover = cover
            self.episodes = episodes or []

    website_model.Episode = Episode
    website_model.SubtitleGroup = SubtitleGroup
    website_model.WebsiteBangumi = WebsiteBangumi

    class BaseWebsite:
        @staticmethod
        def save_bangumi(data):
            return None

    website_base.BaseWebsite = BaseWebsite

    class ScriptBase:
        class Model:
            obj = None
            bangumi_name = None
            cover = ""
            update_time = "Unknown"
            due_date = None
            source = None
            bangumi_id = None
            subtitle_list = []
            max_page = int(config.get("max_path", 3) or 3)

            def __init__(self):
                self.obj = None

        def __init__(self):
            self.model = self.Model()

        @property
        def name(self):
            return self.Model.bangumi_name

        @property
        def bangumi_name(self):
            return self.Model.bangumi_name

        @property
        def cover(self):
            return self.Model.cover

        @property
        def updated_time(self):
            return self.Model.update_time

        @property
        def source(self):
            return self.Model.source

        def get_download_url(self):
            return {}

    script_mod.ScriptBase = ScriptBase

    # Stubs for scripts which import tables or the fetcher at module level.
    class _Missing:
        @classmethod
        def get(cls, *args, **kwargs):
            raise RuntimeError("BGmi database access is not available inside scripts")

        @classmethod
        def all(cls, *args, **kwargs):
            return []

    class _Table(_Missing):
        pass

    table_mod.Followed = _Table
    table_mod.Scripts = _Table
    table_mod.Bangumi = _Table
    table_mod.Download = _Table
    table_mod.Subtitle = _Table

    constants_mod.SUPPORT_WEBSITE = [
        {"view": "萌番组 https://bangumi.moe/", "id": "bangumi_moe"},
        {"view": "蜜柑计划 https://mikanani.me/", "id": "mikan_project"},
        {"view": "动漫花园 http://share.dmhy.org/", "id": "dmhy"},
    ]
    constants_mod.BANGUMI_UPDATE_TIME = ("Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Unknown")
    constants_mod.SPACIAL_APPEND_CHARS = ["Ⅱ", "Ⅲ", "♪", "Δ", "×", "☆", "É", "·", "♭", "★"]
    constants_mod.SPACIAL_REMOVE_CHARS = []

    _season_re = re.compile(r"第\s*(\d+)\s*季|[Ss]eason\s*(\d+)|(?<![a-zA-Z])S(\d+)(?![a-zA-Z\d])")

    def parse_season(name):
        m = _season_re.search(str(name))
        if not m:
            return 1
        for value in m.groups():
            if value:
                return int(value)
        return 1

    def strip_season_suffix(name):
        return re.sub(
            r"(?:[\s._\-:：/／|｜]+)?(?:第\s*\d+\s*季|[Ss]eason\s*\d+|(?<![a-zA-Z])S\d+(?![a-zA-Z\d]))\s*$",
            "",
            str(name),
        ).strip()

    season_mod.parse_season = parse_season
    season_mod.strip_season_suffix = strip_season_suffix
    bgmi.__version__ = "1.0.0"

    class _Website:
        def __getattr__(self, item):
            raise RuntimeError("data source access from scripts is handled by bgmi-go")

    fetch_mod.website = _Website()

    for name, module in {
        "bgmi": bgmi,
        "bgmi.script": script_mod,
        "bgmi.utils": utils_mod,
        "bgmi.config": config_mod,
        "bgmi.lib": lib_mod,
        "bgmi.lib.fetch": fetch_mod,
        "bgmi.lib.table": table_mod,
        "bgmi.lib.constants": constants_mod,
        "bgmi.lib.season": season_mod,
        "bgmi.website": website_mod,
        "bgmi.website.base": website_base,
        "bgmi.website.model": website_model,
    }.items():
        sys.modules[name] = module


def _json_safe(value):
    if isinstance(value, (str, int, float, bool)) or value is None:
        return value
    if isinstance(value, (datetime.datetime, datetime.date)):
        return value.isoformat()
    if isinstance(value, dict):
        return {str(k): _json_safe(v) for k, v in value.items()}
    if isinstance(value, (list, tuple, set)):
        return [_json_safe(v) for v in value]
    return str(value)


def _load_scripts(script_path):
    scripts = []
    for path in sorted(glob.glob(os.path.join(script_path, "*.py"))):
        try:
            module_name = "bgmi_user_script_%d" % len(scripts)
            spec = importlib.util.spec_from_file_location(module_name, path)
            if spec is None or spec.loader is None:
                raise RuntimeError("cannot create module spec")
            module = importlib.util.module_from_spec(spec)
            spec.loader.exec_module(module)
            script = module.Script()
        except Exception as exc:  # noqa: BLE001
            scripts.append((path, None, "%s: %s" % (type(exc).__name__, exc)))
            continue
        scripts.append((path, script, None))
    return scripts


def main():
    mode = sys.argv[1] if len(sys.argv) > 1 else "meta"
    script_path = sys.argv[2] if len(sys.argv) > 2 else ""
    _install_shim()

    real_stdout = sys.stdout
    sys.stdout = sys.stderr

    results = []
    for path, script, error in _load_scripts(script_path):
        item = {"file": path, "ok": error is None, "error": error, "model": None, "urls": {}}
        if error is None:
            model = script.Model()
            due_date = getattr(model, "due_date", None)
            if due_date is not None and due_date < datetime.datetime.now():
                item["ok"] = False
                item["skipped"] = True
                item["error"] = "due_date reached"
                results.append(item)
                continue
            item["model"] = {
                "bangumi_name": getattr(model, "bangumi_name", None),
                "cover": getattr(model, "cover", "") or "",
                "update_time": getattr(model, "update_time", "Unknown") or "Unknown",
                "source": getattr(model, "source", None),
                "bangumi_id": getattr(model, "bangumi_id", None),
                "subtitle_list": list(getattr(model, "subtitle_list", []) or []),
                "max_page": int(getattr(model, "max_page", 3) or 3),
                "due_date": _json_safe(due_date),
            }
            if mode == "run":
                try:
                    urls = script.get_download_url() or {}
                    item["urls"] = {str(k): str(v) for k, v in dict(urls).items()}
                except Exception as exc:  # noqa: BLE001
                    item["ok"] = False
                    item["error"] = "%s: %s" % (type(exc).__name__, exc)
        results.append(item)

    print(json.dumps(results, ensure_ascii=False), file=real_stdout)


if __name__ == "__main__":
    main()
