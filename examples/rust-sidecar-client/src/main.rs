use llmkit_sidecar_test_client::Client;
use std::env;
use std::io;
use std::os::unix::net::UnixStream;

fn main() -> io::Result<()> {
    let arguments: Vec<String> = env::args().collect();
    if arguments.len() != 3 {
        eprintln!("usage: llmkit-sidecar-test-client SOCKET SESSION_KEY");
        std::process::exit(2);
    }
    let stream = UnixStream::connect(&arguments[1])?;
    let mut client = Client::new(stream, &arguments[2]);
    let handshake = client.handshake("rust-example")?;
    if handshake.protocol_version != "1.0" {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "protocol negotiation failed",
        ));
    }
    println!("{}", client.health()?);
    Ok(())
}
