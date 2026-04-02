package testutil

// SampleDriveListOutput simulates makemkvcon -r --cache=1 info disc:9999 output
// showing three drives: one with a disc, one empty, one not attached.
const SampleDriveListOutput = `DRV:0,2,999,1,"BD-RE HL-DT-ST BD-RE  WH16NS40","DEADPOOL_2","/dev/sr0"
DRV:1,0,999,0,"BD-RE ASUS BW-16D1HT","","/dev/sr1"
DRV:2,256,999,0,"","",""
`

// SampleDiscInfoOutput simulates makemkvcon -r info dev:/dev/sr0 output
// with 3 titles and a selection of streams per title.
const SampleDiscInfoOutput = `MSG:1005,0,0,"MakeMKV v1.17.7 linux(x64-release) started","","MakeMKV v1.17.7 linux(x64-release) started"
TCOUT:3
CINFO:1,0,"Blu-ray disc"
CINFO:2,0,"DEADPOOL_2"
TINFO:0,2,0,"Deadpool 2"
TINFO:0,8,0,"27"
TINFO:0,9,0,"1:59:45"
TINFO:0,10,0,"53.4 GB"
TINFO:0,11,0,"57344761856"
TINFO:0,16,0,"00100.mpls"
TINFO:0,27,0,"title_t00.mkv"
TINFO:0,33,0,"/dev/sr0"
SINFO:0,0,1,0,"V_MPEG4/ISO/AVC"
SINFO:0,0,2,0,"Chapters"
SINFO:0,1,1,0,"A_TRUEHD"
TINFO:1,2,0,"Deadpool 2 (Extended Cut)"
TINFO:1,8,0,"29"
TINFO:1,9,0,"2:05:12"
TINFO:1,10,0,"56.7 GB"
TINFO:1,11,0,"60897845248"
TINFO:1,16,0,"00200.mpls"
TINFO:1,27,0,"title_t01.mkv"
TINFO:1,33,0,"/dev/sr0"
SINFO:1,0,1,0,"V_MPEG4/ISO/AVC"
SINFO:1,1,1,0,"A_TRUEHD"
TINFO:2,2,0,"Bonus Features"
TINFO:2,8,0,"1"
TINFO:2,9,0,"0:05:22"
TINFO:2,10,0,"1.2 GB"
TINFO:2,11,0,"1288490188"
TINFO:2,16,0,"00300.mpls"
TINFO:2,27,0,"title_t02.mkv"
TINFO:2,33,0,"/dev/sr0"
SINFO:2,0,1,0,"V_MPEG4/ISO/AVC"
MSG:1005,0,1,"Operation successfully completed","","Operation successfully completed"
`

// SampleProgressOutput simulates PRGV progress lines during a rip operation.
const SampleProgressOutput = `PRGV:0,0,65536
PRGV:125,1000,65536
PRGV:32768,50000,65536
PRGV:65536,65536,65536
MSG:1005,0,1,"Operation successfully completed","","Operation successfully completed"
`

// EmptyDiscOutput simulates makemkvcon robot-mode output when a disc is
// inserted but contains no readable titles (e.g. a scratched or unrecognised
// disc). There is a DRV line for the drive but zero TINFO lines.
const EmptyDiscOutput = `MSG:1005,0,0,"MakeMKV v1.17.7 linux(x64-release) started","","MakeMKV v1.17.7 linux(x64-release) started"
DRV:0,2,999,12,"BD-RE HL-DT-ST BD-RE  WH16NS40","UNKNOWN_DISC","/dev/sr0"
TCOUT:0
CINFO:1,0,"Blu-ray disc"
CINFO:2,0,"UNKNOWN_DISC"
MSG:1005,0,1,"Operation successfully completed","","Operation successfully completed"
`

// NoDiscMsgOutput simulates makemkvcon robot-mode output when the drive is
// empty or the disc cannot be read, represented by a MSG:5055 error line
// (no disc / drive not ready).
const NoDiscMsgOutput = `MSG:5055,516,0,"Failed to open disc","","Failed to open disc"
`
