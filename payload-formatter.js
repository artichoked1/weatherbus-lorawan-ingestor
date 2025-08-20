function decodeUplink(input) {
  const bytes = input.bytes;
  const FORMAT_LEN = {
	0: 1, // SENSORBUS_FMT_UINT8
	1: 2, // SENSORBUS_FMT_UINT16
	2: 4, // SENSORBUS_FMT_FLOAT32
	3: 8, // SENSORBUS_FMT_FLOAT64
	4: 2, // SENSORBUS_FMT_UFIX16_1DP
	5: 2, // SENSORBUS_FMT_UFIX16_1DP
	  
  };

  const slaves = [];
  const warnings = [];
  const errors = [];
  let pos = 0;

  while (pos + 3 <= bytes.length) {
	const sid = (bytes[pos] << 8) | bytes[pos + 1];
	pos += 2;
	const sensorCount = bytes[pos++];
	const sensors = [];

	for (let i = 0; i < sensorCount; i++) {
	  if (pos + 2 > bytes.length) {
		errors.push('Truncated sensor header');
		return { data: { slaves }, warnings, errors };
	  }
	  const type = bytes[pos++];
	  const hdr = bytes[pos++];
	  const fmt = hdr >> 5;
	  const index = hdr & 0x1F;
	  const len = FORMAT_LEN[fmt];
	  if (!len) {
		errors.push(`Unknown format ${fmt}`);
		return { data: { slaves }, warnings, errors };
	  }
	  if (pos + len > bytes.length) {
		errors.push('Truncated sensor value');
		return { data: { slaves }, warnings, errors };
	  }

	  const raw = bytes.slice(pos, pos + len);
	  pos += len;

	  const dv = new DataView(new Uint8Array(raw).buffer);
	  let value;
	  switch (fmt) {
		case 0:
		  value = dv.getUint8(0);
		  break;
		case 1:
		  value = dv.getUint16(0, true);
		  break;
		case 2:
		  value = dv.getFloat32(0, true);
		  break;
		case 3:
		  value = dv.getFloat64(0, true);
		  break;
		case 4:
		  value = dv.getInt16(0, true) / 100;
		  break;
		case 5:
		  value = dv.getUint16(0, true) / 10;
		  break;
		default:
		  value = raw;
	  }

	  sensors.push({ type, index, format: fmt, value });
	}

	slaves.push({ id: sid, sensors });
  }

  if (pos !== bytes.length) {
	warnings.push('Extra bytes at end of payload');
  }

  return { data: { slaves }, warnings, errors };
}

if (typeof module !== 'undefined') {
  module.exports = { decodeUplink };
}