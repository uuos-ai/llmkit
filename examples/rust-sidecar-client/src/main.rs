use std::env;
use std::io::{self, Read, Write};
use std::os::unix::net::UnixStream;

const MAX_FRAME: usize = 8 << 20;

fn write_frame<W: Write>(writer: &mut W, body: &[u8]) -> io::Result<()> {
    if body.is_empty() || body.len() > MAX_FRAME {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "invalid frame length",
        ));
    }
    writer.write_all(&(body.len() as u32).to_be_bytes())?;
    writer.write_all(body)
}

fn read_frame<R: Read>(reader: &mut R) -> io::Result<Vec<u8>> {
    let mut header = [0_u8; 4];
    reader.read_exact(&mut header)?;
    let length = u32::from_be_bytes(header) as usize;
    if length == 0 || length > MAX_FRAME {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "invalid frame length",
        ));
    }
    let mut body = vec![0_u8; length];
    reader.read_exact(&mut body)?;
    Ok(body)
}

fn json_string(value: &str) -> String {
    let mut result = String::with_capacity(value.len() + 2);
    result.push('"');
    for character in value.chars() {
        match character {
            '"' => result.push_str("\\\""),
            '\\' => result.push_str("\\\\"),
            '\n' => result.push_str("\\n"),
            '\r' => result.push_str("\\r"),
            '\t' => result.push_str("\\t"),
            c if c.is_control() => result.push_str(&format!("\\u{:04x}", c as u32)),
            c => result.push(c),
        }
    }
    result.push('"');
    result
}

fn request(key: &str, id: &str, method: &str, payload: &str) -> String {
    format!(
        "{{\"version\":\"1.0\",\"session_key\":{},\"request_id\":{},\"method\":{},\"payload\":{}}}",
        json_string(key),
        json_string(id),
        json_string(method),
        payload
    )
}

fn main() -> io::Result<()> {
    let arguments: Vec<String> = env::args().collect();
    if arguments.len() != 3 {
        eprintln!("usage: llmkit-sidecar-test-client SOCKET SESSION_KEY");
        std::process::exit(2);
    }
    let mut stream = UnixStream::connect(&arguments[1])?;
    let handshake = request(
        &arguments[2],
        "handshake",
        "handshake",
        "{\"supported_versions\":[\"1.0\"]}",
    );
    write_frame(&mut stream, handshake.as_bytes())?;
    let response = String::from_utf8(read_frame(&mut stream)?)
        .map_err(|_| io::Error::new(io::ErrorKind::InvalidData, "response is not UTF-8"))?;
    if !response.contains("\"type\":\"result\"") {
        return Err(io::Error::new(
            io::ErrorKind::PermissionDenied,
            "handshake failed",
        ));
    }
    write_frame(
        &mut stream,
        request(&arguments[2], "health", "health", "null").as_bytes(),
    )?;
    println!("{}", String::from_utf8_lossy(&read_frame(&mut stream)?));
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Cursor;

    #[test]
    fn framing_round_trip() {
        let mut bytes = Vec::new();
        write_frame(&mut bytes, b"hello").unwrap();
        assert_eq!(read_frame(&mut Cursor::new(bytes)).unwrap(), b"hello");
    }

    #[test]
    fn escapes_session_key() {
        assert_eq!(json_string("a\"b\\c"), "\"a\\\"b\\\\c\"");
    }
}
