//! Test-only stopped-child handoff: no product can exec before cgroup readback.
use super::{Process, private};
use serde_json::{Value, json};
use std::{
    fs,
    os::unix::fs::MetadataExt,
    path::{Path, PathBuf},
    process::{Command, Stdio},
    time::{Duration, Instant},
};

pub struct Plan {
    ready: PathBuf,
    helper: PathBuf,
}

impl Plan {
    pub fn from_environment() -> Option<Self> {
        match (
            std::env::var_os("LMM_COMMERCE_RESOURCE_READY"),
            std::env::var_os("LMM_COMMERCE_RESOURCE_HELPER"),
        ) {
            (None, None) => None,
            (Some(ready), Some(helper)) => {
                let plan = Self {
                    ready: ready.into(),
                    helper: helper.into(),
                };
                for path in [&plan.ready, &plan.helper] {
                    assert!(path.is_absolute() && path.is_file());
                    assert_eq!(path.canonicalize().unwrap(), *path);
                }
                Some(plan)
            }
            _ => panic!("resource ready file and helper must be supplied together"),
        }
    }

    pub fn command(&self, binary: &Path) -> Command {
        let mut command = Command::new("/bin/sh");
        command
            .args([
                "-c",
                "kill -STOP \"$$\" || exit 125; exec \"$@\"",
                "lmm-resource",
            ])
            .arg(binary);
        command
    }

    // @mark runtime.joint-resource role=child
    pub fn attach(&self, process: &Process, binary: &Path, expected: &str) {
        let pid = process.0.id();
        let deadline = Instant::now() + Duration::from_secs(5);
        let before = loop {
            let fields = proc_fields(pid);
            if fields[0] == "T" {
                break fields;
            }
            assert!(
                Instant::now() < deadline,
                "bootstrap did not stop before exec"
            );
            std::thread::sleep(Duration::from_millis(5));
        };
        let role = if process.3.starts_with("core") {
            "core"
        } else {
            "extensions"
        };
        let result = Command::new("/usr/bin/python3")
            .env_clear()
            .env("PATH", "/usr/bin:/bin")
            .arg("-B")
            .arg(&self.helper)
            .arg("--group-ready")
            .arg(&self.ready)
            .arg("--pid")
            .arg(pid.to_string())
            .arg("--role")
            .arg(role)
            .arg("--binary")
            .arg(binary)
            .arg("--sha256")
            .arg(expected)
            .stdin(Stdio::null())
            .output()
            .unwrap();
        private(
            &process.2,
            &format!("{}.scope-attach.log", process.3),
            &result.stderr,
        );
        assert!(
            result.status.success(),
            "scope attach failed; inspect private log"
        );
        let mut lease: Value = serde_json::from_slice(&result.stdout).unwrap();
        assert_eq!(lease["identity"]["pid"], pid);
        assert_eq!(
            lease["identity"]["start_ticks"].as_u64().unwrap(),
            before[19].parse::<u64>().unwrap()
        );
        let after = proc_fields(pid);
        for index in [0, 1, 2, 3, 19] {
            assert_eq!(
                after[index], before[index],
                "stopped child identity changed"
            );
        }
        lease["process_group"] = json!(before[2].parse::<u32>().unwrap());
        lease["session"] = json!(before[3].parse::<u32>().unwrap());
        let parent: Value = serde_json::from_slice(&fs::read(&self.ready).unwrap()).unwrap();
        lease["parent_cgroup"] = parent["cgroup"].clone();
        private(
            &process.2,
            &format!("{}.resource.json", process.3),
            &serde_json::to_vec(&lease).unwrap(),
        );
        verify(process);
        assert!(
            Command::new("/usr/bin/kill")
                .env_clear()
                .args(["-CONT", "--", &pid.to_string()])
                .status()
                .unwrap()
                .success()
        );
        let wanted = fs::metadata(binary).unwrap();
        let deadline = Instant::now() + Duration::from_secs(5);
        loop {
            let actual = fs::metadata(format!("/proc/{pid}/exe")).unwrap();
            if (actual.dev(), actual.ino()) == (wanted.dev(), wanted.ino()) {
                break;
            }
            assert!(
                Instant::now() < deadline,
                "direct child did not exec frozen product"
            );
            std::thread::sleep(Duration::from_millis(5));
        }
        verify(process);
    }
}

fn proc_fields(pid: u32) -> Vec<String> {
    fs::read_to_string(format!("/proc/{pid}/stat"))
        .unwrap()
        .rsplit_once(") ")
        .unwrap()
        .1
        .split_whitespace()
        .map(str::to_owned)
        .collect()
}

pub fn verify(process: &Process) {
    let path = process.2.join(format!("{}.resource.json", process.3));
    if !path.exists() {
        assert!(Plan::from_environment().is_none(), "resource lease missing");
        return;
    }
    let lease: Value = serde_json::from_slice(&fs::read(path).unwrap()).unwrap();
    let pid = process.0.id();
    let fields = proc_fields(pid);
    assert_eq!(
        fields[19].parse::<u64>().unwrap(),
        lease["identity"]["start_ticks"].as_u64().unwrap()
    );
    assert_eq!(
        fields[1].parse::<u32>().unwrap(),
        std::process::id(),
        "product must remain a direct test child"
    );
    assert_eq!(
        fields[2].parse::<u64>().unwrap(),
        lease["process_group"].as_u64().unwrap()
    );
    assert_eq!(
        fields[3].parse::<u64>().unwrap(),
        lease["session"].as_u64().unwrap()
    );
    let child = Path::new(lease["cgroup"].as_str().unwrap());
    let parent = Path::new(lease["parent_cgroup"].as_str().unwrap());
    assert_eq!(child.parent().unwrap(), parent);
    for (group, memory, tasks) in [(parent, "536870912", "256"), (child, "268435456", "64")] {
        for (key, value) in [
            ("cpu.max", "100000 100000"),
            ("memory.max", memory),
            ("memory.swap.max", "0"),
            ("pids.max", tasks),
        ] {
            assert_eq!(
                fs::read_to_string(group.join(key)).unwrap().trim(),
                value,
                "resource hard limit changed"
            );
        }
    }
    let relative = child.strip_prefix("/sys/fs/cgroup").unwrap();
    assert_eq!(
        fs::read_to_string(format!("/proc/{pid}/cgroup"))
            .unwrap()
            .trim(),
        format!("0::/{}", relative.display())
    );
    assert_eq!(
        fs::read_to_string(child.join("cgroup.procs"))
            .unwrap()
            .split_whitespace()
            .collect::<Vec<_>>(),
        [pid.to_string()]
    );
}
