// Independent compilation until the relay owner wires `pub mod routing` in lib.rs.
// Keeping the bridge here avoids changing shared entry points in task 04.
#[path = "../src/routing/mod.rs"]
pub mod routing;
