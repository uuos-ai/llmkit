use serde::{de::DeserializeOwned, Deserialize, Serialize};
use serde_json::Value;
use std::io::{self, Read, Write};

pub const PROTOCOL_VERSION: &str = "1.0";
pub const MAX_FRAME: usize = 8 << 20;

#[derive(Debug, Serialize)]
pub struct Request<'a, T: Serialize> {
    pub version: &'static str,
    pub session_key: &'a str,
    pub request_id: &'a str,
    pub method: &'a str,
    pub payload: T,
}

#[derive(Debug, Deserialize)]
pub struct Response<T> {
    pub version: String,
    pub request_id: String,
    #[serde(rename = "type")]
    pub message_type: String,
    pub payload: Option<T>,
    pub error: Option<ProtocolError>,
}

#[derive(Debug, Deserialize)]
pub struct ProtocolError {
    pub kind: String,
    pub message: String,
    #[serde(default)]
    pub retryable: bool,
    #[serde(default)]
    pub retry_after_ms: i64,
}

#[derive(Debug, Serialize)]
pub struct HandshakeRequest<'a> {
    pub supported_versions: [&'a str; 1],
    pub host_build_id: &'a str,
}

#[derive(Debug, Deserialize)]
pub struct HandshakeResponse {
    pub protocol_version: String,
    pub instance_id: String,
    pub client_id: String,
    #[serde(default)]
    pub client_instance_id: String,
    #[serde(default)]
    pub user_id: String,
    #[serde(default)]
    pub binding_version: u64,
    pub methods: Vec<String>,
}

pub struct Client<S> {
    transport: S,
    token: String,
    next_request: u64,
}

impl<S: Read + Write> Client<S> {
    pub fn new(transport: S, token: impl Into<String>) -> Self {
        Self {
            transport,
            token: token.into(),
            next_request: 1,
        }
    }

    pub fn handshake(&mut self, host_build_id: &str) -> io::Result<HandshakeResponse> {
        self.call(
            "handshake",
            HandshakeRequest {
                supported_versions: [PROTOCOL_VERSION],
                host_build_id,
            },
        )
    }

    pub fn health(&mut self) -> io::Result<Value> {
        self.call("health", Value::Null)
    }

    pub fn available_targets(&mut self) -> io::Result<Value> {
        self.call("get_available_targets", serde_json::json!({}))
    }

    pub fn enroll_client(&mut self) -> io::Result<Value> {
        self.call("enroll_client", Value::Null)
    }

    pub fn call<T: Serialize, R: DeserializeOwned>(
        &mut self,
        method: &str,
        payload: T,
    ) -> io::Result<R> {
        let request_id = format!("rust-{}", self.next_request);
        self.next_request += 1;
        let request = Request {
            version: PROTOCOL_VERSION,
            session_key: &self.token,
            request_id: &request_id,
            method,
            payload,
        };
        let body = serde_json::to_vec(&request)
            .map_err(|_| io::Error::new(io::ErrorKind::InvalidInput, "request encode failed"))?;
        write_frame(&mut self.transport, &body)?;
        let response: Response<R> = serde_json::from_slice(&read_frame(&mut self.transport)?)
            .map_err(|_| io::Error::new(io::ErrorKind::InvalidData, "response decode failed"))?;
        if response.version != PROTOCOL_VERSION || response.request_id != request_id {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "response envelope mismatch",
            ));
        }
        if let Some(error) = response.error {
            return Err(io::Error::new(
                io::ErrorKind::Other,
                format!("{}: {}", error.kind, error.message),
            ));
        }
        if response.message_type != "result" {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "expected unary result",
            ));
        }
        response
            .payload
            .ok_or_else(|| io::Error::new(io::ErrorKind::InvalidData, "result payload is missing"))
    }

    pub fn into_inner(self) -> S {
        self.transport
    }
}

pub fn write_frame<W: Write>(writer: &mut W, body: &[u8]) -> io::Result<()> {
    if body.is_empty() || body.len() > MAX_FRAME {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "invalid frame length",
        ));
    }
    writer.write_all(&(body.len() as u32).to_be_bytes())?;
    writer.write_all(body)
}

pub fn read_frame<R: Read>(reader: &mut R) -> io::Result<Vec<u8>> {
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

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Cursor;

    struct TestTransport {
        read: Cursor<Vec<u8>>,
        written: Vec<u8>,
    }

    impl Read for TestTransport {
        fn read(&mut self, buffer: &mut [u8]) -> io::Result<usize> {
            self.read.read(buffer)
        }
    }
    impl Write for TestTransport {
        fn write(&mut self, buffer: &[u8]) -> io::Result<usize> {
            self.written.extend_from_slice(buffer);
            Ok(buffer.len())
        }
        fn flush(&mut self) -> io::Result<()> {
            Ok(())
        }
    }

    #[test]
    fn framing_round_trip() {
        let mut bytes = Vec::new();
        write_frame(&mut bytes, b"hello").unwrap();
        assert_eq!(read_frame(&mut Cursor::new(bytes)).unwrap(), b"hello");
    }

    #[test]
    fn typed_health_round_trip() {
        let response =
            br#"{"version":"1.0","request_id":"rust-1","type":"result","payload":{"status":"ok"}}"#;
        let mut framed = Vec::new();
        write_frame(&mut framed, response).unwrap();
        let transport = TestTransport {
            read: Cursor::new(framed),
            written: Vec::new(),
        };
        let mut client = Client::new(transport, "token");
        let health = client.health().unwrap();
        assert_eq!(health["status"], "ok");
    }
}
