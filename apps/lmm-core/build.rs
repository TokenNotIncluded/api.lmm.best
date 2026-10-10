fn main() -> Result<(), Box<dyn std::error::Error>> {
    let root = "../../contracts/proto";
    let sources = [
        "../../contracts/proto/lmm/core/v1/control.proto",
        "../../contracts/proto/lmm/core/v1/events.proto",
        "../../contracts/proto/lmm/core/v1/commands.proto",
        "../../contracts/proto/lmm/core/v1/ledger.proto",
        "../../contracts/proto/lmm/core/v1/payments.proto",
    ];
    for source in sources {
        println!("cargo:rerun-if-changed={source}");
    }
    let mut config = tonic_prost_build::Config::new();
    // Avoid process-global environment mutation (unsafe in edition 2024).
    config.protoc_executable(protoc_bin_vendored::protoc_bin_path()?);
    tonic_prost_build::configure()
        .build_client(false)
        .compile_with_config(config, &sources, &[root])?;
    Ok(())
}
