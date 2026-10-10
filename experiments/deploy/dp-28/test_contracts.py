"""Unexecuted Docker operations remain explicitly separate from local FS tests."""
import unittest
from docker_plan import preview
from disk_lifecycle import PROJECT, Refused


def image(index, owned=True):
    return {"Id": "sha256:" + format(index, "064x"), "Config": {"Labels": {
        "io.lmm.project": PROJECT if owned else "another-project", "io.lmm.managed-by": "dp28"}}}


def fixture():
    return {"project": PROJECT, "all_containers_included": True,
            "images": [image(i, i != 6) for i in range(1, 8)],
            "containers": [{"Id": "other-project-stopped-container", "Image": image(5)["Id"],
                            "State": {"Running": False}, "Config": {"Labels": {"project": "foreign"}}}],
            "release_images": {"current": [image(1)["Id"]], "previous_verified": [image(2)["Id"]],
                               "inflight": [image(3)["Id"]], "pulling": [image(4)["Id"]], "pinned": []}}


class DockerContractTests(unittest.TestCase):
    def test_only_unreferenced_project_labelled_image_is_a_candidate(self):
        plan = preview(fixture())
        self.assertEqual(plan["image_candidates"], [image(7)["Id"]])
        self.assertEqual(plan["volumes_to_delete"], [])
        self.assertFalse(plan["apply_implemented"])
        self.assertIsNone(plan["reclaimable_physical_bytes"])

    def test_missing_reference_or_filtered_container_inventory_refuses(self):
        incomplete = fixture()
        incomplete["all_containers_included"] = False
        with self.assertRaises(Refused):
            preview(incomplete)
        incomplete = fixture()
        incomplete["release_images"]["previous_verified"] = [image(99)["Id"]]
        with self.assertRaises(Refused):
            preview(incomplete)

    def test_latest_tag_does_not_identify_a_retained_version(self):
        mutable = fixture()
        mutable["release_images"]["current"] = ["lmm-core:latest"]
        with self.assertRaises(Refused):
            preview(mutable)
