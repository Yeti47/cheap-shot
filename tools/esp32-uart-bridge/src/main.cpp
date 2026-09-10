#include <Arduino.h>

void setup() {
  Serial.begin(115200);                      // native USB CDC (the "USB" port) -- open this on the PC
  Serial1.begin(115200, SERIAL_8N1, 18, 17);  // dedicated UART1 on GPIO18=RX, GPIO17=TX -- wired to the camera
}

void loop() {
  while (Serial1.available()) Serial.write(Serial1.read());
  while (Serial.available())  Serial1.write(Serial.read());
}
