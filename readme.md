# usensor


usensor is a simple tool to graph some libsensors temps and fans.


I specifically made this program to tweak fan settings when I install a new
computer because I could not find anything good enough for simple real time
monitoring with graphs that can run headless so it can be used for servers as
well.


It's not really polished to a great end user experience or aimed at being run
constantly (among other things it keeps all data points in memory from program
start and onwards. It needs more work if you want to run it like that.


It's linux only and you need libsensors development headers to link against.
